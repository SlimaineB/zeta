package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"zeta/cli/internal/client"
	"zeta/cli/internal/config"
	"zeta/cli/internal/contexttar"
	"zeta/cli/internal/gitutil"
	"zeta/cli/internal/project"
	syncer "zeta/cli/internal/sync"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "login":
		cmdLogin(os.Args[2:])
	case "logout":
		cmdLogout()
	case "profiles":
		cmdProfiles(os.Args[2:])
	case "build":
		cmdBuild(os.Args[2:])
	case "builds":
		cmdBuilds(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "logs":
		cmdLogs(os.Args[2:])
	case "metrics":
		cmdMetrics(os.Args[2:])
	case "artifacts":
		cmdArtifacts(os.Args[2:])
	case "volumes":
		cmdVolumes(os.Args[2:])
	case "init":
		cmdEnvInit(os.Args[2:])
	case "activate", "up":
		cmdEnvActivate(os.Args[2:])
	case "code":
		cmdEnvCode(os.Args[2:])
	case "deactivate", "down":
		cmdEnvDeactivate(os.Args[2:])
	case "ls":
		cmdEnvList(os.Args[2:])
	case "rm":
		cmdEnvDelete(os.Args[2:])
	case "env":
		cmdEnv(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fatalf("unknown command: %s", os.Args[1])
	}
}

func usage() {
	fmt.Print(`zeta — client Zeta

Usage:
  zeta login [--server URL] [--email EMAIL] [--password PASS] [--cookie COOKIE]
  zeta logout
  zeta profiles
  zeta build [--profile NAME] [--context DIR] [--exclude PATTERN] [--sync]
           [--follow=false] [--artifacts=false] [--artifacts-dir DIR] [--timeout 30m]
  zeta build --git [--repo URL] [--commit REF]  # context from remote Git
  zeta builds [--json]
  zeta status [--json] ID
  zeta logs ID
  zeta metrics ID
  zeta artifacts [-o FILE] ID
  zeta volumes [--json] [--role cache|artifacts|context] [--older-than 720h]
  zeta volumes resize NAME SIZE
  zeta volumes delete NAME
  zeta init [--name NAME] [--profile P] [--context DIR] [--size SIZE]
  zeta activate [--name NAME] [--context DIR] [--exclude PATTERN]
  zeta code [--name NAME] [--context DIR] [--print]
  zeta deactivate [--name NAME] [--context DIR]
  zeta ls [--json]
  zeta rm [NAME] [--name NAME] [--context DIR]

Project file:
  zeta.yaml in the context directory can set:
    name, profile, image, context, size, exclude, env

Variables:
  ZETA_SERVER   portal URL (ex: http://zeta.local)
  ZETA_COOKIE   session cookie (or raw value)
  ZETA_PROFILE  default profile for zeta build

Examples:
  zeta login --server http://zeta.local --email dev@example.com --password dev
  cd my-project
  zeta build
  zeta build --exclude build --exclude .cache
  zeta build --sync  # syncs local changes during build
  zeta logs zeta-cpp-...
  zeta artifacts -o artifacts.tar zeta-cpp-...
  zeta volumes --older-than 720h
  zeta init --profile go          # provision a persistent environment
  zeta activate                   # ssh-like shell + real-time file mirror
  zeta code                       # open VS Code in the browser on /workspace
  zeta deactivate                 # stop the environment pod
`)
}

func cmdLogin(args []string) {
	cfg := mustConfig()
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", cfg.Server, "portal URL")
	email := fs.String("email", "", "email (dev mode)")
	password := fs.String("password", "", "password (dev mode)")
	cookie := fs.String("cookie", "", "existing session cookie (OIDC mode)")
	_ = fs.Parse(args)

	if strings.TrimSpace(*server) == "" {
		fatalf("--server or ZETA_SERVER required")
	}
	if strings.TrimSpace(*cookie) != "" {
		cfg.Server = strings.TrimRight(*server, "/")
		cfg.Cookie = strings.TrimSpace(*cookie)
		if err := config.Save(cfg); err != nil {
			fatalf("session save: %v", err)
		}
		fmt.Println("session saved")
		return
	}
	if strings.TrimSpace(*email) == "" {
		*email = prompt("Email: ")
	}
	if *password == "" {
		*password = prompt("Password: ")
	}
	c, err := client.New(*server, "")
	if err != nil {
		fatalf("%v", err)
	}
	session, err := c.Login(context.Background(), strings.TrimSpace(*email), *password)
	if err != nil {
		fatalf("login: %v", err)
	}
	cfg.Server = strings.TrimRight(*server, "/")
	cfg.Cookie = session
	if err := config.Save(cfg); err != nil {
		fatalf("session save: %v", err)
	}
	fmt.Printf("connected to %s\n", cfg.Server)
}

func cmdLogout() {
	if err := config.Clear(); err != nil {
		fatalf("logout: %v", err)
	}
	fmt.Println("local session removed")
}

func cmdProfiles(args []string) {
	c := mustClient()
	profiles, err := c.Profiles(context.Background())
	if err != nil {
		fatalf("profiles: %v", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tLANGUAGE\tDEFAULT REPO\tDESCRIPTION")
	for _, p := range profiles {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name, p.Language, p.DefaultRepo, p.Description)
	}
	_ = w.Flush()
}

func cmdBuild(args []string) {
	cfg := mustConfig()
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	profile := fs.String("profile", os.Getenv("ZETA_PROFILE"), "profile (auto-detected if empty)")
	contextDir := fs.String("context", ".", "local context directory")
	excludes := &stringList{}
	fs.Var(excludes, "exclude", "pattern to exclude from context (repeatable)")
	artifactExcludes := &stringList{}
	fs.Var(artifactExcludes, "artifact-exclude", "pattern to exclude from downloaded artifacts (repeatable)")
	gitMode := fs.Bool("git", false, "build from a remote Git repository")
	repo := fs.String("repo", "", "Git repository URL (enables Git mode)")
	commit := fs.String("commit", "", "commit or branch (default: current branch)")
	follow := fs.Bool("follow", true, "follow logs in real time")
	download := fs.Bool("artifacts", true, "download and extract artifacts")
	artifactsDir := fs.String("artifacts-dir", "artifacts", "artifacts output directory")
	syncMode := fs.Bool("sync", false, "bidirectional interactive context sync during build")
	timeout := fs.Duration("timeout", 30*time.Minute, "build wait timeout")
	_ = fs.Parse(args)

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	useGit := *gitMode || set["repo"]
	if useGit && set["context"] {
		fatalf("--context and --repo/--git are incompatible")
	}
	if set["commit"] && !useGit {
		fatalf("--commit requires --git or --repo")
	}
	if useGit && *syncMode {
		fatalf("--sync is incompatible with --git/--repo")
	}

	c := mustClientFor(cfg)
	var id string
	var artifactPatterns []string
	var syncSession *syncer.Session
	var syncPatterns []string
	if useGit {
		root, err := gitutil.RepoRoot()
		if err != nil {
			fatalf("%v", err)
		}
		if strings.TrimSpace(*repo) == "" {
			*repo, err = gitutil.RemoteURL()
			if err != nil {
				fatalf("%v", err)
			}
		}
		if strings.TrimSpace(*commit) == "" {
			*commit = gitutil.DefaultCommit()
		}
		if strings.TrimSpace(*profile) == "" {
			*profile, err = detectProfile(c, root)
			if err != nil {
				fatalf("%v", err)
			}
		}
		artifactPatterns = append(profileArtifactExcludes(c, *profile), artifactExcludes.values()...)
		if gitutil.DirtyIgnoring(*artifactsDir) {
			fmt.Fprintln(os.Stderr, "warning: uncommitted local changes; only the pushed commit will be built")
		}
		id, err = c.Submit(context.Background(), client.SubmitRequest{
			Profile: strings.TrimSpace(*profile),
			Repo:    strings.TrimSpace(*repo),
			Commit:  strings.TrimSpace(*commit),
		})
		if err != nil {
			fatalf("build: %v", err)
		}
		fmt.Printf("build %s (%s) %s@%s\n", id, *profile, *repo, *commit)
	} else {
		root, err := filepath.Abs(*contextDir)
		if err != nil {
			fatalf("context: %v", err)
		}
		info, err := os.Stat(root)
		if err != nil {
			fatalf("context: %v", err)
		}
		if !info.IsDir() {
			fatalf("context: %s is not a directory", root)
		}
		if strings.TrimSpace(*profile) == "" {
			*profile, err = detectProfile(c, root)
			if err != nil {
				fatalf("%v", err)
			}
		}
		artifactPatterns = append(profileArtifactExcludes(c, *profile), artifactExcludes.values()...)
		tmp, err := os.CreateTemp("", "zeta-context-*.tar.gz")
		if err != nil {
			fatalf("context: %v", err)
		}
		contextPath := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(contextPath)
		patterns := append(append(contexttar.DefaultExcludes(), *artifactsDir), excludes.values()...)
		syncPatterns = patterns
		size, err := contexttar.WriteFile(contextPath, root, patterns)
		if err != nil {
			fatalf("context: %v", err)
		}
		fmt.Fprintf(os.Stderr, "context: %s (%s)\n", root, formatBytes(size))
		id, err = c.SubmitContext(context.Background(), client.SubmitRequest{
			Profile: strings.TrimSpace(*profile),
			Repo:    "local:" + root,
			Sync:    *syncMode,
		}, contextPath)
		if err != nil {
			fatalf("build: %v", err)
		}
		fmt.Printf("build %s (%s) context %s\n", id, *profile, root)
		if *syncMode {
			syncURL, err := c.SyncURL(id)
			if err != nil {
				fatalf("sync: %v", err)
			}
			syncSession, err = syncer.StartClient(context.Background(), syncer.ClientOptions{
				URL:            syncURL,
				Cookie:         cfg.Cookie,
				Workspace:      root,
				Artifacts:      filepath.Join(*artifactsDir, id),
				Ignore:         syncPatterns,
				ArtifactIgnore: artifactPatterns,
			})
			if err != nil {
				fatalf("sync: %v", err)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if syncSession != nil {
		readyCtx, cancelReady := context.WithTimeout(ctx, 2*time.Minute)
		err := syncSession.WaitReady(readyCtx)
		cancelReady()
		if err != nil {
			_ = syncSession.Close()
			fatalf("sync: initialisation: %v", err)
		}
		fmt.Println("sync: initial context synced")
	}

	if *follow {
		fmt.Println("--- logs ---")
		if err := c.StreamLogs(ctx, id, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "logs: %v\n", err)
		}
	}

	build, err := waitBuild(ctx, c, id)
	if err != nil {
		if syncSession != nil {
			_ = syncSession.Close()
		}
		fatalf("waiting for build: %v", err)
	}
	if syncSession != nil {
		flushCtx, cancelFlush := context.WithTimeout(context.Background(), 10*time.Second)
		if err := syncSession.Flush(flushCtx); err != nil {
			fmt.Fprintf(os.Stderr, "sync: flush: %v\n", err)
		}
		cancelFlush()
		_ = syncSession.Close()
		syncSession = nil
	}
	if build.Status != "succeeded" {
		fmt.Fprintf(os.Stderr, "build failed in %s\n", formatDuration(build.DurationMs))
		os.Exit(1)
	}
	fmt.Printf("build succeeded in %s\n", formatDuration(build.DurationMs))
	printCache(ctx, c, id)

	if *download {
		dest := filepath.Join(*artifactsDir, id)
		if !*syncMode {
			if err := extractArtifacts(ctx, c, id, dest, artifactPatterns); err != nil {
				fatalf("artifacts: %v", err)
			}
		}
		abs, _ := filepath.Abs(dest)
		fmt.Printf("artifacts: %s\n", abs)
	}
}

type stringList struct {
	items []string
}

func (s *stringList) String() string { return strings.Join(s.items, ",") }

func (s *stringList) Set(v string) error {
	s.items = append(s.items, v)
	return nil
}

func (s *stringList) values() []string { return s.items }

func cmdBuilds(args []string) {
	fs := flag.NewFlagSet("builds", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	c := mustClient()
	builds, err := c.Builds(context.Background())
	if err != nil {
		fatalf("builds: %v", err)
	}
	if *asJSON {
		writeJSON(builds)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPROFILE\tREPO\tDURATION")
	for _, b := range builds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s@%s\t%s\n", b.ID, b.Status, b.Profile, b.Repo, b.Commit, formatDuration(b.DurationMs))
	}
	_ = w.Flush()
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	id := firstArg(fs.Args(), "status")
	c := mustClient()
	builds, err := c.Builds(context.Background())
	if err != nil {
		fatalf("status: %v", err)
	}
	for _, b := range builds {
		if b.ID != id {
			continue
		}
		if *asJSON {
			writeJSON(b)
			return
		}
		fmt.Printf("id:        %s\n", b.ID)
		fmt.Printf("status:    %s\n", b.Status)
		fmt.Printf("profile:    %s (%s)\n", b.Profile, b.Language)
		fmt.Printf("repo:     %s\n", b.Repo)
		fmt.Printf("commit:    %s\n", b.Commit)
		fmt.Printf("owner: %s\n", b.Owner)
		fmt.Printf("created:      %s\n", b.CreatedAt)
		fmt.Printf("duration:     %s (checkout %s, build %s)\n", formatDuration(b.DurationMs), formatDuration(b.InitDurationMs), formatDuration(b.BuildDurationMs))
		return
	}
	fatalf("build not found: %s", id)
}

func cmdLogs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	_ = fs.Parse(args)
	id := firstArg(fs.Args(), "logs")
	c := mustClient()
	if err := c.StreamLogs(context.Background(), id, os.Stdout); err != nil {
		fatalf("logs: %v", err)
	}
}

func cmdMetrics(args []string) {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	_ = fs.Parse(args)
	id := firstArg(fs.Args(), "metrics")
	c := mustClient()
	raw, err := c.Metrics(context.Background(), id)
	if err != nil {
		fatalf("metrics: %v", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		fmt.Println(string(raw))
		return
	}
	fmt.Println(pretty.String())
}

func cmdVolumes(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "resize":
			cmdVolumeResize(args[1:])
			return
		case "delete":
			cmdVolumeDelete(args[1:])
			return
		}
	}
	fs := flag.NewFlagSet("volumes", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	role := fs.String("role", "", "filter by role (cache, artifacts, context)")
	olderThan := fs.Duration("older-than", 0, "show volumes unused since this duration")
	_ = fs.Parse(args)
	c := mustClient()
	resp, err := c.Volumes(context.Background())
	if err != nil {
		fatalf("volumes: %v", err)
	}
	volumes := resp.Volumes
	filtered := make([]client.Volume, 0, len(volumes))
	for _, v := range volumes {
		if *role != "" && v.Role != *role {
			continue
		}
		if *olderThan > 0 {
			if v.LastUsedAt == nil || time.Since(*v.LastUsedAt) < *olderThan {
				continue
			}
		}
		filtered = append(filtered, v)
	}
	if *asJSON {
		writeJSON(filtered)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tROLE\tSIZE\tLAST USE\tBUILD")
	for _, v := range filtered {
		last := "never"
		if v.LastUsedAt != nil {
			last = v.LastUsedAt.Local().Format("2006-01-02 15:04") + " (" + formatAge(time.Since(*v.LastUsedAt)) + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", v.Name, v.Role, formatBytes(v.CapacityBytes), last, v.LastBuild)
	}
	_ = w.Flush()
	if resp.Quota.MaxTotalBytes > 0 {
		fmt.Printf("\nquota: %s / %s used, %s free (max per volume: %s)\n",
			formatBytes(resp.Quota.UsedBytes), formatBytes(resp.Quota.MaxTotalBytes),
			formatBytes(resp.Quota.RemainingBytes), formatBytes(resp.Quota.MaxVolumeBytes))
	}
}

func cmdVolumeResize(args []string) {
	if len(args) < 2 {
		fatalf("usage: zeta volumes resize NAME SIZE")
	}
	c := mustClient()
	if err := c.ResizeVolume(context.Background(), args[0], args[1]); err != nil {
		fatalf("resize: %v", err)
	}
	fmt.Printf("volume %s resized to %s\n", args[0], args[1])
}

func cmdVolumeDelete(args []string) {
	if len(args) < 1 {
		fatalf("usage: zeta volumes delete NOM")
	}
	c := mustClient()
	if err := c.DeleteVolume(context.Background(), args[0]); err != nil {
		fatalf("delete: %v", err)
	}
	fmt.Printf("volume %s deleted\n", args[0])
}

func cmdEnv(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: zeta init|activate|deactivate|ls|rm ...")
		return
	}
	switch args[0] {
	case "init":
		cmdEnvInit(args[1:])
	case "activate", "up":
		cmdEnvActivate(args[1:])
	case "code":
		cmdEnvCode(args[1:])
	case "deactivate", "down":
		cmdEnvDeactivate(args[1:])
	case "ls", "list":
		cmdEnvList(args[1:])
	case "rm", "delete":
		cmdEnvDelete(args[1:])
	default:
		fatalf("unknown env command: %s", args[0])
	}
}

func cmdEnvInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	name := fs.String("name", "", "environment name (default: zeta.yaml or context directory name)")
	profile := fs.String("profile", os.Getenv("ZETA_PROFILE"), "profile (auto-detected if empty)")
	contextDir := fs.String("context", ".", "local context directory")
	size := fs.String("size", "", "volume size (e.g. 5Gi)")
	_ = fs.Parse(args)
	c := mustClient()
	proj, root, err := project.Load(*contextDir)
	if err != nil {
		fatalf("zeta.yaml: %v", err)
	}
	p := firstNonEmpty(*profile, proj.Profile)
	if p == "" {
		p, err = detectProfile(c, root)
		if err != nil {
			fatalf("%v", err)
		}
	}
	id := envName(firstNonEmpty(*name, proj.Name), root)
	env, err := c.CreateEnv(context.Background(), client.EnvRequest{
		Name:    id,
		Profile: p,
		Image:   proj.Image,
		Size:    firstNonEmpty(*size, proj.Size),
		Env:     map[string]string(proj.Env),
	})
	if err != nil {
		fatalf("init: %v", err)
	}
	fmt.Printf("environment %s created (profile %s, %s)\n", env.ID, env.Profile, formatBytes(env.SizeBytes))
	fmt.Printf("run: zeta activate --name %s\n", env.ID)
}

func cmdEnvActivate(args []string) {
	fs := flag.NewFlagSet("activate", flag.ExitOnError)
	name := fs.String("name", "", "environment name (default: zeta.yaml or context directory name)")
	contextDir := fs.String("context", ".", "local context directory")
	excludes := &stringList{}
	fs.Var(excludes, "exclude", "pattern to exclude from sync (repeatable)")
	_ = fs.Parse(args)
	c := mustClient()
	cfg := mustConfig()
	proj, root, err := project.Load(*contextDir)
	if err != nil {
		fatalf("zeta.yaml: %v", err)
	}
	id := envName(firstNonEmpty(*name, proj.Name), root)
	if err := c.ActivateEnv(context.Background(), id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") && !strings.Contains(err.Error(), "zeta init") {
			fatalf("activate: environment %s not found; run 'zeta init' first", id)
		}
		fatalf("activate: %v", err)
	}
	syncURL, err := c.SyncURL(id)
	if err != nil {
		fatalf("sync: %v", err)
	}
	execURL, err := c.EnvExecURL(id)
	if err != nil {
		fatalf("exec: %v", err)
	}
	artifactsDir, err := os.MkdirTemp("", "zeta-env-artifacts-*")
	if err != nil {
		fatalf("sync: %v", err)
	}
	defer os.RemoveAll(artifactsDir)
	patterns := append(contexttar.EnvExcludes(), proj.Exclude...)
	patterns = append(patterns, excludes.values()...)
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	session, err := syncer.StartClient(ctx, syncer.ClientOptions{
		URL:       syncURL,
		Cookie:    cfg.Cookie,
		Workspace: root,
		Artifacts: artifactsDir,
		Ignore:    patterns,
	})
	if err != nil {
		fatalf("sync: %v", err)
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, 3*time.Minute)
	err = session.WaitReady(readyCtx)
	cancelReady()
	if err != nil {
		_ = session.Close()
		fatalf("sync: initialisation: %v", err)
	}
	fmt.Printf("environment %s active; mirroring %s\n", id, root)
	fmt.Println("Ctrl-D or 'exit' leaves the shell; the environment stays up")
	conn, err := syncer.DialExec(ctx, execURL, cfg.Cookie)
	if err != nil {
		_ = session.Close()
		fatalf("exec: %v", err)
	}
	if err := syncer.RunShell(ctx, conn, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "shell: %v\n", err)
	}
	_ = conn.Close()
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 15*time.Second)
	if err := session.Flush(flushCtx); err != nil {
		fmt.Fprintf(os.Stderr, "sync: flush: %v\n", err)
	}
	cancelFlush()
	_ = session.Close()
	fmt.Printf("environment %s still active; run 'zeta deactivate --name %s' to stop it\n", id, id)
}

func cmdEnvCode(args []string) {
	fs := flag.NewFlagSet("code", flag.ExitOnError)
	name := fs.String("name", "", "environment name (default: zeta.yaml or context directory name)")
	contextDir := fs.String("context", ".", "local context directory")
	printOnly := fs.Bool("print", false, "print the URL without opening a browser")
	_ = fs.Parse(args)
	c := mustClient()
	proj, root, err := project.Load(*contextDir)
	if err != nil {
		fatalf("zeta.yaml: %v", err)
	}
	id := envName(firstNonEmpty(*name, proj.Name), root)
	if err := c.ActivateEnv(context.Background(), id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") && !strings.Contains(err.Error(), "zeta init") {
			fatalf("code: environment %s not found; run 'zeta init' first", id)
		}
		fatalf("code: %v", err)
	}
	url, err := c.EnableEnvIDE(context.Background(), id)
	if err != nil {
		fatalf("code: %v", err)
	}
	fmt.Println(url)
	if *printOnly {
		return
	}
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		fmt.Printf("ouvrez cette URL dans votre navigateur: %s\n", url)
	}
}

func cmdEnvDeactivate(args []string) {
	fs := flag.NewFlagSet("deactivate", flag.ExitOnError)
	name := fs.String("name", "", "environment name (default: zeta.yaml or context directory name)")
	contextDir := fs.String("context", ".", "local context directory")
	_ = fs.Parse(args)
	c := mustClient()
	proj, root, err := project.Load(*contextDir)
	if err != nil {
		fatalf("zeta.yaml: %v", err)
	}
	id := envName(firstNonEmpty(*name, proj.Name), root)
	if err := c.DeactivateEnv(context.Background(), id); err != nil {
		fatalf("deactivate: %v", err)
	}
	fmt.Printf("environment %s deactivated\n", id)
}

func cmdEnvList(args []string) {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	c := mustClient()
	envs, err := c.Environments(context.Background())
	if err != nil {
		fatalf("ls: %v", err)
	}
	if *asJSON {
		writeJSON(envs)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPROFILE\tACTIVE\tSIZE\tSTORAGE CLASS")
	for _, env := range envs {
		active := "no"
		if env.Active {
			active = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", env.ID, env.Profile, active, formatBytes(env.SizeBytes), env.StorageClass)
	}
	_ = w.Flush()
}

func cmdEnvDelete(args []string) {
	name := ""
	rest := args
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		name = rest[0]
		rest = rest[1:]
	}
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	flagName := fs.String("name", name, "environment name")
	contextDir := fs.String("context", ".", "local context directory")
	_ = fs.Parse(rest)
	c := mustClient()
	proj, root, err := project.Load(*contextDir)
	if err != nil {
		fatalf("zeta.yaml: %v", err)
	}
	id := envName(firstNonEmpty(*flagName, proj.Name), root)
	if err := c.DeleteEnv(context.Background(), id); err != nil {
		fatalf("rm: %v", err)
	}
	fmt.Printf("environment %s deleted\n", id)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func envName(flagName, contextDir string) string {
	if value := strings.TrimSpace(flagName); value != "" {
		return envSlug(value)
	}
	abs, err := filepath.Abs(contextDir)
	if err != nil {
		abs = contextDir
	}
	return envSlug(filepath.Base(abs))
}

func envSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "env"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func absDir(dir string) string {
	root, err := filepath.Abs(dir)
	if err != nil {
		fatalf("context: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		fatalf("context: %v", err)
	}
	if !info.IsDir() {
		fatalf("context: %s is not a directory", root)
	}
	return root
}

func cmdArtifacts(args []string) {
	fs := flag.NewFlagSet("artifacts", flag.ExitOnError)
	out := fs.String("o", "", "output file")
	_ = fs.Parse(args)
	id := firstArg(fs.Args(), "artifacts")
	if *out == "" {
		*out = id + ".tar"
	}
	c := mustClient()
	f, err := os.Create(*out)
	if err != nil {
		fatalf("artifacts: %v", err)
	}
	if err := c.StreamArtifact(context.Background(), id, f); err != nil {
		_ = f.Close()
		_ = os.Remove(*out)
		fatalf("artifacts: %v", err)
	}
	if err := f.Close(); err != nil {
		fatalf("artifacts: %v", err)
	}
	abs, _ := filepath.Abs(*out)
	fmt.Printf("artifacts written to %s\n", abs)
}

func printCache(ctx context.Context, c *client.Client, id string) {
	raw, err := c.Metrics(ctx, id)
	if err != nil {
		return
	}
	var m struct {
		Hits      int64   `json:"hits"`
		Misses    int64   `json:"misses"`
		HitRate   float64 `json:"hitRate"`
		SizeBytes int64   `json:"sizeBytes"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	var parts []string
	if m.Hits+m.Misses > 0 {
		parts = append(parts, fmt.Sprintf("%d hits / %d miss (%.0f%%)", m.Hits, m.Misses, m.HitRate))
	}
	if m.SizeBytes > 0 {
		parts = append(parts, formatBytes(m.SizeBytes))
	}
	if len(parts) > 0 {
		fmt.Printf("cache: %s\n", strings.Join(parts, " · "))
	}
}

func profileArtifactExcludes(c *client.Client, name string) []string {
	profiles, err := c.Profiles(context.Background())
	if err != nil {
		return nil
	}
	for _, p := range profiles {
		if p.Name == name {
			return p.ArtifactExclude
		}
	}
	return nil
}

func detectProfile(c *client.Client, root string) (string, error) {
	lang := detectLanguage(root)
	profiles, err := c.Profiles(context.Background())
	if err != nil {
		return "", fmt.Errorf("profiles: %w", err)
	}
	if lang != "" {
		for _, p := range profiles {
			if p.Language == lang || p.Name == lang {
				return p.Name, nil
			}
		}
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	if lang == "" {
		return "", fmt.Errorf("profile cannot be detected; use --profile (%s)", strings.Join(names, ", "))
	}
	return "", fmt.Errorf("no profile for language %s; available: %s", lang, strings.Join(names, ", "))
}

func detectLanguage(root string) string {
	switch {
	case exists(filepath.Join(root, "CMakeLists.txt")):
		return "cpp"
	case exists(filepath.Join(root, "go.mod")):
		return "go"
	case exists(filepath.Join(root, "pyproject.toml")),
		exists(filepath.Join(root, "setup.py")),
		exists(filepath.Join(root, "requirements.txt")):
		return "python"
	default:
		return ""
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func waitBuild(ctx context.Context, c *client.Client, id string) (client.Build, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		builds, err := c.Builds(ctx)
		if err != nil {
			return client.Build{}, err
		}
		found := false
		for _, b := range builds {
			if b.ID != id {
				continue
			}
			found = true
			if b.Status == "succeeded" || b.Status == "failed" {
				return b, nil
			}
		}
		if !found {
			return client.Build{}, fmt.Errorf("build not found: %s", id)
		}
		select {
		case <-ctx.Done():
			return client.Build{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func extractArtifacts(ctx context.Context, c *client.Client, id, dest string, excludes []string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "zeta-artifacts-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := c.StreamArtifact(ctx, id, tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		return err
	}
	defer tmp.Close()
	return extractTar(tmp, dest, excludes)
}

func extractTar(r io.Reader, dest string, excludes []string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == "" {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("invalid artifact path: %s", hdr.Name)
		}
		if contexttar.Match(filepath.ToSlash(name), hdr.FileInfo().IsDir(), excludes) {
			continue
		}
		target := filepath.Join(dest, name)
		rel, err := filepath.Rel(dest, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("artifact path outside destination: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}

func mustConfig() config.Config {
	cfg, err := config.Load()
	if err != nil {
		fatalf("configuration: %v", err)
	}
	return cfg.WithEnv()
}

func mustClient() *client.Client {
	return mustClientFor(mustConfig())
}

func mustClientFor(cfg config.Config) *client.Client {
	c, err := client.New(cfg.Server, cfg.Cookie)
	if err != nil {
		fatalf("%v", err)
	}
	if strings.TrimSpace(cfg.Cookie) == "" {
		fatalf("session missing; run zeta login")
	}
	return c
}

func firstArg(args []string, cmd string) string {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fatalf("usage: zeta %s ID", cmd)
	}
	return strings.TrimSpace(args[0])
}

func prompt(label string) string {
	fmt.Print(label)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	n := float64(bytes)
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if n >= 10 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func formatAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func formatDuration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	if ms < 60000 {
		return fmt.Sprintf("%.1f s", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm %ds", ms/60000, (ms%60000)/1000)
}

func writeJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatalf("json: %v", err)
	}
	fmt.Println(string(data))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "zeta: "+format+"\n", args...)
	os.Exit(1)
}

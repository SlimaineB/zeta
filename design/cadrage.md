# Zeta — Cadrage MVP

Document de cadrage à valider avant tout développement.
Dernière mise à jour : 2026-11-08

---

## 1. Problème

Les utilisateurs réservent des machines très dimensionnées pour des builds ponctuels.
Ces machines restent allumées et coûtent cher alors que le build ne dure que quelques minutes.

Objectif : offrir un service de build à la demande qui crée des pods éphémères à partir
d'images de base préconstruites, injecte le contexte du projet, streame les logs en temps
réel, produit des artefacts et (si demandé) une image OCI, puis détruit tout.
L'utilisateur ne manipule jamais de kubeconfig.

## 2. Objectifs

- Soumettre un build via API, CLI ou Web UI.
- Exécuter chaque build dans un pod éphémère isolé à partir d'une **image de base préconstruite**.
- Injecter le contexte du projet dans le pod (volume) en delta (Git/CDC), sans reconstruire
  d'image par job.
- Afficher les logs en temps réel (terminal + navigateur).
- Produire des artefacts téléchargeables et, si demandé, une image OCI via BuildKit.
- Permettre d'ouvrir un **VS Code Web** sur le workspace du job via un proxy bastion
  authentifié, sans kubeconfig et sans exposer les pods directement.
- Gérer plusieurs langages / versions d'outils via un catalogue d'images de base par utilisateur.
- Appliquer des quotas (concurrence, CPU/RAM, durée, taille) pour maîtriser les coûts.
- Ne jamais exposer Kubernetes à l'utilisateur : auth par token/OIDC, RBAC porté par le control plane.
- Éteindre les nœuds de build quand ils sont inutilisés (autoscaling à zéro, spot).

## 3. Non-objectifs (v1)

- Multi-cluster / multi-région.
- Builds privilégiés, accès au host, Docker-in-Docker privilégié.
- Exposition directe des pods ou de ports arbitraires sur Internet.
- IDE persistant après suppression du pod (v1 : session liée à la durée de vie du job).
- Support Windows.
- SaaS public multi-organisations.
- Remplacer une CI complète (pas de pipelines, pas de triggers Git intégrés en v1).

## 4. Personas et parcours

### Utilisateur développeur
1. `zeta login` (token ou OIDC) — aucune config k8s.
2. `zeta build --base-image go1.23 --context . -- make build` (ajoute `--push` pour exporter une image).
3. Suit les logs : `zeta logs <build-id> -f`.
4. Ouvre un VS Code Web sur le job : `zeta ide <build-id>` (session limitée dans le temps).
5. Récupère les artefacts (`zeta artifacts <build-id>`) et/ou l'image (`registry.local/tenant/app:tag`).

### Admin plateforme
- Gère les quotas par tenant, le catalogue d'images, la rétention des logs/artefacts.
- Surveille coûts, files d'attente et échecs via le Web UI.

## 5. Décisions d'architecture

| Sujet | Décision | Justification |
|---|---|---|
| Control plane | Go + client-go | Écosystème k8s natif, binaire unique, watch/streaming robustes |
| Exécution | Image de base préconstruite + contexte monté | Pas de build d'image par job : démarrage rapide, coût réduit |
| Moteur OCI | BuildKit rootless, uniquement si export d'image | Pas de daemon permanent ; BuildKit seulement quand nécessaire |
| Accès IDE | Sidecar VS Code Web + proxy bastion authentifié | Debug interactif sans kubeconfig ni exposition directe des pods |
| Interface | CLI + Web UI + API REST | CLI pour la CI/dev, UI pour l'observation, API comme contrat |
| Sortie | Artefacts + image OCI optionnelle | La majorité des builds produisent des artefacts |
| Auth utilisateurs | Token API (haché en base), OIDC optionnel | Pas de kubeconfig, intégration CI simple |
| Accès cluster | ServiceAccount dédié au control plane | RBAC minimal, jamais exposé aux users |
| État | PostgreSQL (métadonnées) | Historique, quotas, audit, idempotence |
| Contexte | Git (SHA + patch) ou CDC desync → S3/MinIO (URLs présignées) | Transfert delta minimal, pas de kubeconfig |
| Artefacts | S3/MinIO (URLs présignées) | Découplage, pas de stockage local durable |
| Cache | NVMe local (chaud) + PVC RWO (tiède) + S3/MinIO (froid) ; registry réservé aux images | Évite de retélécharger contexte et dépendances, maîtrise de l'egress |
| Logs | Pod logs API → proxy SSE/NDJSON → S3 | Temps réel + conservation post-mortem |

### Alternatives écartées
- **Construire une image par job** : trop lourd (pull/build/push à chaque fois), démarrage lent et coût inutile.
- **Daemon BuildKit partagé** : moins cher à froid, mais isolation et sécurité insuffisantes.
- **Kaniko** : archivé par Google en juin 2025, fork Chainguard en maintenance seule, images non publiées librement ; écarté pour un nouveau projet.
- **Buildah** : alternative crédible à BuildKit, mais cache/performances inférieurs pour notre cas ; gardé en solution de secours.
- **Tekton / Argo Workflows** : moteurs de pipelines complets, mais ils n'apportent ni l'API multi-tenant, ni l'auth sans kubeconfig, ni le bastion IDE ; surdimensionnés pour un job unique.
- **DevSpace / Skaffold / Tilt / Garden** : boucles de développement côté client nécessitant un kubeconfig ; hors périmètre.
- **Bazel RBE (Buildbarn/BuildGrid)** : excellent pour les monorepos Bazel, mais spécifique à un système de build ; profil ultérieur, pas le socle générique.
- **Jobs Kubernetes bruts exposés aux users** : nécessite kubeconfig et RBAC complexe.

> Étude détaillée des alternatives, verdicts et sources : **Annexe A**.
> Conclusion : composer avec des briques existantes (Jobs, BuildKit, code-server, Karpenter)
> et n'écrire que la couche multi-tenant/API/bastion.

## 6. Architecture cible

```
┌────────────┐   ┌────────────┐   ┌────────────┐
│    CLI     │   │   Web UI   │   │   CI/CD    │
└─────┬──────┘   └─────┬──────┘   └─────┬──────┘
      │  HTTPS/SSE/WS  │                │
      └────────────────┼────────────────┘
                       ▼
              ┌─────────────────┐
              │  Control Plane  │  (Go, in-cluster)
               │  API / Auth     │
               │  Job Manager    │
               │  Context/Cache  │
               │  Log Proxy      │
              │  IDE Proxy      │◄──── navigateur (VS Code Web)
              │  Quota / GC     │
              └───┬─────────┬───┘
                  │         │
     ┌────────────▼──┐  ┌───▼─────────────┐
     │ Kubernetes API│  │ Postgres / S3   │
     └────────┬──────┘  └─────────────────┘
              │ crée des Jobs/Pods éphémères
              ▼
   ┌────────────────────────────────────┐
   │ Namespace tenant (NetworkPolicy)   │
    │  ┌──────────────────────────────┐  │
    │  │ Pod build (TTL court)        │  │
    │  │  conteneur = image de base   │  │
    │  │  workspace = contexte monté  │  │
    │  │  runner = commande user      │  │
    │  │  buildkitd optionnel (export)│  │
    │  │  sidecar VS Code Web (opt.)  │  │
    │  └──────────────────────────────┘  │
    │  DaemonSet cache NVMe (hot tier)   │
    └────────────────────────────────────┘
              │ push (optionnel)
              ▼
        Registry OCI
```

### Composants du control plane

- **API REST** : soumission, statut, annulation, artefacts, catalogue d'images.
- **Auth** : tokens API, OIDC, tenants, rôles (user/admin).
- **Job Manager** : file d'attente, création de Jobs k8s, suivi d'état, timeouts.
- **Log Proxy** : `pods/log?follow=true` → SSE (UI) / NDJSON (CLI), upload S3.
- **IDE Proxy (bastion)** : authentifie l'utilisateur, vérifie l'appartenance du job,
  puis proxifie HTTP/WebSocket vers le sidecar VS Code via `port-forward` Kubernetes.
  Aucun Service/Ingress n'expose le pod directement.
- **Artifact Broker** : URLs présignées S3 (upload/download), validation de taille.
- **Context Service** : descripteurs Git/CDC, miroir Git par projet, manifeste de chunks,
  URLs présignées ; ne stocke jamais le contexte complet en base.
- **Cache Manager** : politique hot/warm/cold, index des chunks, quotas, GC, anti-poisoning.
- **Image Catalog** : images de base par tenant (alias → ref + outils/versions).
- **Quota Manager** : concurrence, CPU/RAM, durée max, taille artefacts, budget.
- **GC** : suppression pods/jobs/logs/artefacts/cache expirés.

## 7. Modèle d'exécution d'un build

### Principe : pas d'image construite par job
Le build s'exécute **dans une image de base préconstruite** appartenant au tenant.
Le contexte du projet est téléchargé puis monté dans un volume de travail (`/workspace`) :
il n'est jamais copié dans l'image. On évite ainsi un cycle pull/build/push à chaque job,
ce qui réduit fortement le temps de démarrage et le coût.

### Deux modes
1. **Commande (défaut)** : image de base du catalogue + contexte + commande
   (`make`, `cargo build`, `npm run build`…). Produit des artefacts.
2. **Export OCI (optionnel)** : si l'utilisateur demande une image, un conteneur
   BuildKit rootless démarre dans le même pod, construit depuis le workspace et pousse
   l'image. BuildKit n'est pas démarré pour les builds d'artefacts.

> **BuildKit = moteur d'images OCI**, pas le runner par défaut. Pour un build d'artefacts
> (`make`, `cargo build`, `npm run build`…), la commande tourne directement dans le
> conteneur de l'image de base. BuildKit n'intervient que si l'on demande une image
> (`--push` / Dockerfile). Il sait aussi produire des fichiers via LLB, mais ce serait
> plus complexe que d'utiliser directement le conteneur.

### Transfert ultra-rapide du contexte

Objectif : ne transférer que ce qui a changé, et servir depuis le disque local quand c'est possible.

**Deux chemins selon la source**
1. **Contexte Git (recommandé)** : le CLI envoie `repo_url`, `commit_sha` et, si le
   working tree est sale, un patch binaire (`git diff --binary`, fichiers non suivis
   inclus via `git add -N`). Le pod récupère les objets manquants depuis un miroir Git
   par projet (service Git interne ou PVC, avec cache local), puis applique le patch.
   Transfert typique : quelques Ko à quelques Mo au lieu du repo complet.
   Si le patch dépasse le seuil, basculer sur le chemin CDC.
2. **Contexte non-Git / gros binaires** : découpage **content-defined chunking (CDC)**
   type casync/desync, chunks zstd adressés par SHA-256. Le CLI n'envoie que les chunks
   absents du store ; l'index est minuscule.

**Reassemblage côté pod**
- Init container : récupère le manifeste puis les objets/chunks manquants dans cet ordre :
  1. cache local du nœud (NVMe, DaemonSet) ;
  2. store objet S3/MinIO ou miroir Git (GET/clone parallèles) ;
  3. option P2P Dragonfly pour les gros contextes multi-nœuds.
- Extraction parallèle (`desync extract`) dans un `emptyDir` ou un PVC de workspace.
- Si un workspace précédent du même projet est présent, il sert de **seed** :
  seuls les chunks absents sont écrits.
- Montage paresseux (post-MVP) : `desync mount-index` / FUSE pour démarrer le build
  avant la fin du téléchargement.

**Optimisations**
- Seuil : CDC seulement au-delà de ~50 Mo ou pour les contextes binaires ; sinon `tar.zst`.
- zstd niveau 3–6, chunks 16–256 Ko (moyenne 64 Ko), upload/download parallèles (8–16).
- Cache de chunks côté CLI : le second upload d'un même repo est quasi nul.
- Déduplication par contenu ; jamais de base64 dans le JSON.
- Sous-modules et LFS : transférer les références/URLs, pas les objets déjà versionnés.
- Ne pas reconstruire le contexte complet à chaque job.

### Cache partagé à coût maîtrisé

**Trois niveaux**

| Niveau | Support | Contenu | Coût / usage |
|---|---|---|---|
| Chaud | NVMe local du nœud (DaemonSet, best-effort) | chunks de contexte, couches d'images, caches de langage, BuildKit | très rapide, perdu si le nœud part ; pas de durabilité |
| Tiède | PVC RWO par tenant/projet (NVMe/block) | dépendances (`~/.cache`, `node_modules`, `target`, `.gradle`, `go/pkg/mod`) | persistant, attaché à une zone/nœud ; éviter RWX |
| Froid | S3/MinIO | chunks, cache BuildKit S3, sccache, cache Bazel/Gradle, artefacts | pas cher, durable, latence réseau |

**Règles**
- Cache BuildKit : `type=s3` (ou registry) avec `mode=min` par défaut ; `mode=max`
  seulement pour les étapes intermédiaires coûteuses. Compression + GC.
- Cache registry : réservé aux couches d'images ; rétention stricte (le registry coûte
  plus cher que S3).
- Cache de dépendances : PVC par projet ou proxy de cache partagé (Athens, Verdaccio,
  devpi, Nexus, sccache S3). Ne pas partager un cache mutable entre tenants.
- Clés : `tenant/project/toolchain/base_image_digest/lockfile_hash`. Le partage porte sur
  les objets **immuables** (chunks, couches, sorties content-addressed), au sein d'un tenant ;
  les caches mutables restent isolés par projet.
- PR non fiables : cache en lecture seule ou namespace de cache séparé, promotion après
  merge (anti-empoisonnement).
- GC : TTL, quota par tenant, LRU, `prune` des chunks non référencés.
- Cache nœud : servi par le DaemonSet (pas de hostPath dans le pod), clés par hash,
  LRU et quota par tenant ; chiffrement au repos si le support le permet.
- Coût : NVMe chaud pour la vitesse, S3 pour la durabilité, registry seulement si
  nécessaire. Dragonfly si beaucoup de nœuds/gros volumes pour éviter l'egress.

**Recommandation v1** : Git-first (SHA + patch), CDC desync en repli pour le non-Git ;
cache NVMe local + S3 (pas de Dragonfly au départ) ; PVC RWO uniquement pour les caches
de dépendances par projet ; BuildKit S3 `mode=min` pour l'export OCI. Ajouter Dragonfly
seulement si les métriques montrent un egress ou un fan-out problématique.

### Cycle de vie
`queued → scheduling → running → succeeded | failed | cancelled | timed_out`

1. Client envoie un descripteur de contexte : Git (`repo_url` + `commit_sha` + patch éventuel)
   ou manifeste CDC + liste des chunks manquants.
2. Control plane valide quotas, résout l'alias d'image, crée le Job k8s.
3. Pod : init récupère le contexte (cache local → S3/Git → option P2P), l'extrait ;
   le runner exécute la commande dans l'image de base.
4. Logs streamés en continu ; artefacts uploadés S3 ; image poussée seulement si demandée.
5. Fin : statut persisté, logs archivés, pod supprimé (TTL), cache local best-effort conservé,
   chunks S3 conservés selon TTL.
6. En cas de dépassement de durée : annulation et nettoyage.

### Isolation et sécurité
- Conteneurs non privilégiés, `allowPrivilegeEscalation: false` ; BuildKit rootless en mode export.
- Capabilities supprimées, seccomp `RuntimeDefault`, rootfs read-only si possible.
- `emptyDir` pour workspace/socket ; pas de hostPath utilisateur. Le cache NVMe est exposé
  par un DaemonSet (HTTP/gRPC local ou CSI local PV), jamais monté directement depuis l'hôte.
- Resource requests/limits + `ephemeral-storage` limités.
- NetworkPolicy par namespace tenant : egress autorisé vers registry/S3/miroirs de paquets,
  **blocage explicite de l'endpoint metadata cloud (169.254.169.254) et des services cluster**.
- Un namespace par tenant, `imagePullSecrets` et quotas gérés par le control plane.
- ServiceAccount du control plane avec RBAC limité aux namespaces tenants.
- Pods de build avec `automountServiceAccountToken: false`.
- Port IDE non exposé par Service/Ingress : accessible uniquement via le proxy bastion.

## 8. Accès IDE (VS Code Web) via bastion

### Principe
Un utilisateur peut ouvrir un VS Code Web sur le workspace de son job pour debugger
un build en cours. Le navigateur ne contacte jamais Kubernetes : il passe par un proxy
bastion authentifié qui relaie HTTP et WebSocket vers le sidecar IDE du pod.

### Exécution
- Le sidecar `code-server` (ou OpenVSCode Server) est ajouté au pod **si l'IDE est demandé**
  (`ide: true`) ou activé par défaut selon la politique du tenant.
- Il partage le volume `/workspace` avec le conteneur de build.
- Il écoute sur `127.0.0.1:8080` ; aucun Service ni Ingress ne l'expose.
- Le control plane ouvre un `port-forward` Kubernetes vers ce port et sert de reverse proxy.
  RBAC requis : `pods/portforward` dans les namespaces tenants.
- L'image du sidecar IDE est fournie par la plateforme, version épinglée (pas d'image user).
- Le sidecar n'a pas de token de ServiceAccount et n'accède pas à l'API Kubernetes.

### Sécurité de la session
- Ouverture réservée au propriétaire du build (ou admin du tenant).
- Jeton de session court (handshake 5–15 min), lié au build et à l'utilisateur.
- Cookie/URL à usage unique ; le jeton n'est jamais écrit dans les logs.
- Durée maximale de session et déconnexion après inactivité configurables.
- Une seule session active par build (v1), fermeture automatique à la fin du pod.
- Audit : ouverture, fermeture, durée, erreurs du proxy.
- Extensions : marketplace désactivé par défaut ; extensions préinstallées dans l'image
  de base ou allowlist. Télémétrie désactivée.

### Limitations v1
- Session liée à la durée de vie du pod : pas d'IDE après suppression du job.
- Pas d'exposition de ports arbitraires ni de terminal SSH générique.
- Coût du sidecar (CPU/RAM) compté dans le quota du tenant.

## 9. Coûts et dimensionnement

- Nœuds de build dédiés (taints/tolerations) en pool spot.
- Karpenter ou cluster-autoscaler configuré pour scale-to-zero.
- Aucun daemon de build permanent (pas de BuildKit partagé) ; le DaemonSet de cache NVMe
  est léger et ne tourne que sur les nœuds de build provisionnés.
- Sidecar IDE optionnel : surcoût CPU/RAM compté dans le quota du tenant.
- Cache à trois niveaux : NVMe local (vitesse), PVC RWO (dépendances), S3 (durabilité).
  Le registry n'est utilisé que pour les couches d'images, pas comme cache générique.
- Options d'optimisation (post-MVP) : pool chaud de petite taille, cache registry
  `mode=min` si export OCI, priorisation des jobs, budget par tenant.
- Coût egress : privilégier le cache local et le P2P (Dragonfly) pour les gros contextes
  et les images de base ; sinon S3 en région.
- Métriques suivies : durée, CPU/heure, taux d'échec, temps d'attente, taille cache,
  taux de hit (local/S3), octets transférés par build.

## 10. API (brouillon)

Auth : `Authorization: Bearer <token>`

| Méthode | Route | Rôle |
|---|---|---|
| POST | `/v1/builds` | Créer un build (JSON + contexte) |
| GET | `/v1/builds` | Lister les builds du tenant |
| GET | `/v1/builds/{id}` | Statut détaillé |
| DELETE | `/v1/builds/{id}` | Annuler |
| GET | `/v1/builds/{id}/logs?follow=true` | Logs (SSE ou NDJSON) |
| POST | `/v1/builds/{id}/ide` | Ouvrir une session IDE (URL + jeton court) |
| GET | `/v1/builds/{id}/ide` | Statut de la session IDE |
| DELETE | `/v1/builds/{id}/ide` | Fermer la session IDE |
| GET/WS | `/v1/ide/{session}` | Proxy HTTP/WebSocket vers le sidecar (auth requise) |
| GET | `/v1/builds/{id}/artifacts` | Lister les artefacts |
| POST | `/v1/builds/{id}/artifacts/{name}/url` | URL de téléchargement présignée |
| POST | `/v1/contexts/git` | Enregistrer un descripteur Git (repo, commit, patch éventuel) |
| POST | `/v1/contexts/chunks` | Envoyer un manifeste CDC ; renvoie les chunks manquants + URLs présignées |
| PUT | `/v1/contexts/{id}/chunks/{hash}` | Upload d'un chunk (URL présignée) |
| GET/POST/DELETE | `/v1/images` | Catalogue d'images de base du tenant |
| GET | `/v1/quotas` | Quotas et consommation |

Exemple de création (build d'artefacts) :

```json
{
  "mode": "command",
  "base_image": "go1.23",
  "context": {"type": "git", "repo": "https://git.local/acme/app", "commit": "a1b2c3d", "patch_key": null},
  "command": ["make", "build"],
  "artifacts": ["bin/app"],
  "ide": true,
  "timeout_seconds": 1800,
  "idempotency_key": "ci-1234"
}
```

Pour un contexte non-Git, remplacer `context` par
`{"type": "chunks", "manifest_key": "contexts/tenant/abc.caidx", "root_hash": "..."}` ;
le CLI obtient d'abord la liste des chunks manquants via `/v1/contexts/chunks`.

Pour exporter une image, ajouter `"push": {"registry": "registry.local/tenant/app", "tag": "v42"}`
(et éventuellement `"dockerfile": "Dockerfile"`). Cela déclenche BuildKit dans le pod.

## 11. CLI (MVP implémenté)

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
zeta profiles
cd mon-projet
zeta build
```

`zeta build` est le parcours principal : détection du profil, envoi du contexte
local en `tar.gz`, logs en temps réel, attente de la fin, téléchargement et
extraction des artefacts dans `artifacts/<id>`, code de sortie `0`/`1`.

`zeta build --sync` ajoute une synchronisation bidirectionnelle interactive via un
sidecar `syncd` et un tunnel WebSocket relayé par le portail : contexte initial,
modifications locales pendant le build, fichiers du workspace et artefacts
renvoyés à la CLI. Les sorties de build (`build`, `dist`, `.venv`, etc.) sont
exclues par défaut. Le mode Git reste disponible avec `--git`/`--repo`.

Commandes avancées :

```bash
zeta builds
zeta status <build-id>
zeta logs <build-id>
zeta metrics <build-id>
zeta artifacts <build-id> -o artefacts.tar
zeta logout
```

Le descripteur Git + patch et le découpage CDC/desync restent des évolutions
ultérieures. Le CLI actuel envoie `profile`, `repo`, `commit` et, en mode
contexte, l’archive du répertoire local.

## 12. Web UI (MVP)

- Login OIDC direct (ou mode dev local) : aucun kubeconfig utilisateur.
- **Dashboard de suivi** : les builds se lancent depuis la CLI `zeta`, pas depuis l'UI.
- Liste des builds (statut, profil, dépôt/commit, durée totale, checkout, build).
- Cartes de synthèse : total, en cours, taux de succès, durée moyenne, cache hit moyen.
- Métriques de cache par build (`ccache_hits`, `ccache_misses`, `cache_bytes`, hit rate).
- Logs temps réel dans une modale, téléchargement des artefacts.
- Rafraîchissement automatique ; UI responsive sans framework front.
- Parcours développeur : `zeta login`, puis `zeta build` depuis le dépôt ; logs en direct et artefacts extraits automatiquement.

### 12.1 Portail MVP (implémenté)

Architecture Go, dépendances dirigées vers le domaine :

```text
portal/
  cmd/zeta/          wiring (config, auth, builds, kube, httpapi)
  internal/domain/        Build, CacheStats, Profile, BuildRepository
  internal/config/        flags + env ZETA_*, validation
  internal/builds/        profils YAML, service, parsing des métriques
  internal/kube/          adapter Kubernetes (Jobs, PVC, logs, artefacts)
  internal/auth/          OIDC + sessions HMAC + mode dev
  internal/httpapi/       routes HTTP, session, erreurs JSON
  internal/ui/            assets embarqués (index, login, app.js, style.css)
```

Déploiement : `platform/manifests/portal.yaml` (ServiceAccount, Role/RoleBinding limités
au namespace, Deployment non-root `65532`, Service). Le portail tourne in-cluster et
utilise son ServiceAccount ; l'utilisateur n'a jamais de kubeconfig.

Commandes utiles :

```bash
cd portal
make test
make deploy          # build + image + import k3s + apply + rollout
kubectl --context local -n zeta-poc port-forward svc/zeta-portal 8080:80
```

Accès ingress : `http://zeta.local` (ajouter l'ADDRESS de l'Ingress
`zeta-portal` dans `/etc/hosts`). En local, `kubectl port-forward` suffit.

WSL sans systemd : après un redémarrage, relancer k3s manuellement :

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'
kubectl --context local get nodes
```

### 12.2 Profils de build personnalisables

Les profils sont chargés depuis `internal/builds/defaults.yaml`, surchargés par
`--profiles-file` ou `ZETA_PROFILES_FILE`. Un profil décrit l'image, le dépôt par
défaut, le répertoire de travail, le cache, les ressources, les commandes et les
métriques. Les commandes peuvent produire des métriques avec
`echo "ZETA_METRIC nom=valeur"`.

Un profil peut aussi déclarer `env` (variables littérales) et
`envFromSecrets` (Secrets Kubernetes optionnels injectés via `envFrom`). Le
Secret `zeta-build-secrets` est référencé par les profils `cpp`, `python` et
`go` ; il permet de fournir `GIT_TOKEN`, `GIT_USERNAME`, `GOPRIVATE`, `GOPROXY`
ou d'autres credentials sans modifier les images. Les profils livrés couvrent
C++/CMake, Python/uv et Go modules, avec caches persistants.

```yaml
profiles:
  - name: cpp
    description: C++ CMake/Ninja avec cache ccache persistant
    language: cpp
    image: zeta/cpp:0.1
    defaultRepo: git://git-server/cpp.git
    workDir: /workspace
    artifactPath: /artifacts/cpp
    cacheDir: /cache/ccache
    envFromSecrets:
      - zeta-build-secrets
    resources:
      cpuRequest: 200m
      memoryRequest: 256Mi
      cpuLimit: "2"
      memoryLimit: 2Gi
      ephemeralStorage: 2Gi
    commands:
      - |
        export CCACHE_DIR=/cache/ccache
        cmake -S . -B build -G Ninja -DCMAKE_BUILD_TYPE=Release
        cmake --build build
        cmake --install build --prefix /artifacts/cpp
    metrics:
      - name: ccache_hits
        command: ccache -s | awk '/Hits:/ {print $2}'
      - name: cache_bytes
        command: du -sb /cache/ccache | cut -f1
```

### 12.3 API MVP implémentée

| Méthode | Route | Rôle |
|---|---|---|
| POST | `/api/login` | Login dev (si `--oidc-dev`) |
| GET | `/oidc/login` | Début du flow OIDC |
| GET | `/oidc/callback` | Callback OIDC |
| POST | `/api/logout` | Fermer la session |
| GET | `/api/me` | Utilisateur courant |
| GET | `/api/profiles` | Profils disponibles |
| GET | `/api/builds` | Lister les builds du portail |
| POST | `/api/builds` | Soumettre `{profile, repo, commit}` |
| GET | `/api/builds/{id}/logs` | Logs en streaming |
| GET | `/api/builds/{id}/metrics` | Métriques de cache |
| GET | `/api/builds/{id}/artifact` | Télécharger les artefacts (tar) |
| GET | `/healthz` | Santé |

## 13. Modèle de données (indicatif)

- `tenants(id, name, created_at)`
- `users(id, tenant_id, email, role)`
- `api_tokens(id, user_id, token_hash, scopes, expires_at)`
- `base_images(id, tenant_id, alias, image_ref, tools, created_at)`
- `contexts(id, tenant_id, kind, repo_url, commit_sha, patch_key, manifest_key, root_hash, size, created_at)`
- `builds(id, tenant_id, user_id, context_id, state, mode, base_image, command, push_target, timeout, started_at, finished_at, error)`
- `build_logs(build_id, s3_key, size, expires_at)`
- `artifacts(id, build_id, name, s3_key, size, sha256)`
- `ide_sessions(id, build_id, user_id, state, token_hash, created_at, expires_at, last_seen_at)`
- `quotas(tenant_id, max_concurrent, max_cpu, max_mem, max_duration, max_artifact_size)`
- `cache_refs(tenant_id, project, registry_ref, last_used_at)`
- `dependency_caches(tenant_id, project, pvc_name, mount_path, size, last_used_at)`
- `chunk_refs(tenant_id, chunk_hash, size, last_used_at)` (index du store S3, GC)

## 14. Catalogue d'images de base

- Une image par langage/version/outillage, construite et maintenue par l'admin ou par le tenant.
- Les images de base **ne contiennent pas le code projet** : le contexte est monté à l'exécution.
- L'utilisateur référence un **alias** (`go1.23`, `node22`, `python3.12-cuda`), pas une ref brute.
- Le control plane résout alias → image OCI et vérifie qu'elle appartient au tenant.
- Les images doivent contenir les outils communs (git, curl, compilateurs), les dépendances
  système et être compatibles avec un conteneur non privilégié.
- Les images sont construites par une CI dédiée (hors Zeta) et publiées dans le registry.
- Politique de mise à jour et de dépréciation à définir.

## 15. Observabilité

- Logs structurés du control plane.
- Métriques Prometheus : builds par état, durée, attente, échecs, CPU/heure, cache hit,
  sessions IDE actives et durée.
- Traces optionnelles (OpenTelemetry) sur la soumission → exécution.
- Audit : qui a soumis/annulé quoi, accès aux artefacts, ouvertures de sessions IDE.

## 16. Rétention et nettoyage

- Pods/Jobs supprimés dès la fin (+ TTL de sécurité), sessions IDE fermées avec le pod.
- Logs conservés N jours (défaut 30) puis purgés de S3.
- Artefacts conservés N jours ou selon quota tenant.
- Caches nettoyés par politique de taille/ancienneté : LRU sur le NVMe local, TTL sur les
  chunks S3 et les PVC de dépendances, rétention stricte sur le registry.
- Namespaces tenants suspendus si quota dépassé.

## 17. Risques et mitigations

| Risque | Mitigation |
|---|---|
| Cold start / pull d'image trop long | Images de base pré-pull sur les nœuds, pool chaud optionnel, cache PVC |
| Coûts qui dérapent | Quotas stricts, timeouts, scale-to-zero, alertes budget |
| Exfiltration via réseau | NetworkPolicy, blocage metadata cloud, egress allowlist |
| Build malveillant | Rootless, pas de privilèges, isolation namespace, limites ressources |
| Fuite de logs/artefacts | URLs présignées courtes, scoping tenant, chiffrement S3 |
| Abus/exposition de l'IDE | Proxy bastion authentifié, jetons courts, owner-only, pas de SA token |
| Saturation registry | Quotas de push, rétention, tags immuables |
| Contexte volumineux / monorepo lent | Git delta (SHA + patch), CDC, cache NVMe, P2P Dragonfly |
| Empoisonnement du cache partagé | Cache PR isolé/lecture seule, promotion après merge, clés par projet |
| Coût egress/stockage du cache | Tiering NVMe→S3, TTL/quotas, S3 en région, registry réservé aux images |

## 18. Plan MVP (proposé)

1. **Socle** : control plane Go, auth token, Postgres, soumission + Job k8s + statut.
2. **Contexte & exécution** : descripteur Git + CDC (desync), cache local/S3, image de base,
   commande, artefacts vers S3.
3. **Logs** : proxy temps réel SSE/NDJSON + archivage S3.
4. **CLI** : login, build, logs, status, cancel, artifacts.
5. **IDE** : sidecar VS Code Web, proxy bastion, `zeta ide`, bouton Web UI.
6. **Export OCI (optionnel)** : BuildKit rootless dans le pod, push registry.
7. **Catalogue images** : CRUD alias → image, validation tenant.
8. **Web UI** : liste, soumission, logs live, artefacts.
9. **Quotas + GC** : limites, timeouts, nettoyage, métriques de base.

## 19. Questions ouvertes

1. Quel registry cible (Harbor, GHCR, ECR, autre) et politique de tags ?
2. Quel stockage S3/MinIO est disponible, et quelle rétention souhaitez-vous ?
3. Quel provider/cluster k8s et quel autoscaler (Karpenter, cluster-autoscaler) ?
4. Quel IdP pour l'OIDC (ou tokens API uniquement au début) ?
5. Quels répertoires de cache de dépendances persister (PVC) par langage ?
6. Qui construit et maintient les images de base (admin central vs équipes) ?
7. Egress réseau : liste blanche de registries/miroirs ou accès Internet complet ?
8. Besoin de builds GPU ou architectures multi-plateformes (arm64) ?
9. Budget cible par mois et nombre de builds simultanés attendu ?
10. Les utilisateurs doivent-ils pouvoir consulter les builds des autres membres de leur tenant ?
11. Les builds produisent-ils souvent une image, ou principalement des artefacts ?
    (impacte la priorité du mode export OCI)
12. L'IDE doit-il être disponible uniquement pendant le build, ou aussi après un échec
    (nécessite alors un workspace persistant type PVC) ?
13. Marketplace d'extensions VS Code autorisé, ou extensions préinstallées uniquement ?
14. Durée maximale et timeout d'inactivité souhaités pour une session IDE ?
15. Le bastion doit-il être intégré au control plane ou déployé séparément
    (segmentation réseau) ?
16. Taille moyenne et nature des contextes (repo Git léger, monorepo, gros binaires) ?
    Cela fixe le seuil d'activation du CDC et l'intérêt de Dragonfly.
17. Contrainte de coût stockage/egress (S3, registry, inter-région) et budget cache ?
18. Les builds partent-ils d'un commit poussé, ou faut-il supporter les modifications
    locales non commitées (patch) dès la v1 ?

---

## Décisions validées

- [x] Stack control plane : Go + client-go
- [x] Exécution : image de base préconstruite + contexte monté, pas d'image construite par job
- [x] BuildKit rootless uniquement pour l'export OCI optionnel
- [x] Interfaces : CLI + Web UI + API REST
- [x] Sorties : artefacts par défaut, image OCI en option
- [x] IDE : VS Code Web en sidecar, accès via proxy bastion authentifié (sans kubeconfig)
- [x] Contexte : chemin Git (SHA + patch) prioritaire, CDC (desync) pour le non-Git/gros binaires
- [x] Cache : NVMe local (chaud) + PVC RWO (tiède) + S3/MinIO (froid) ; registry réservé aux images
- [x] Approche : composer avec l'existant, n'écrire que la couche multi-tenant/API/bastion
- [x] Auth MVP : OIDC direct + sessions serveur, mode dev local pour les tests
- [x] Configuration : profils YAML surchargeables, ressources et métriques par profil
- [x] Déploiement : portail in-cluster avec ServiceAccount/RBAC, aucun kubeconfig utilisateur
- [x] Déclenchement : CLI `zeta build` depuis le dépôt ; l'UI web est un dashboard de suivi

---

## Annexe A — État de l'art et alternatives (nov. 2026)

Cette annexe compare les briques existantes pour éviter de réécrire un moteur de CI,
un moteur de build, un IDE web ou un proxy d'accès. Les informations proviennent des
documentations officielles et de comparatifs 2026 ; elles sont à revérifier avant choix.

### A.1 Décomposition du besoin

Le projet n'est pas un outil unique mais l'assemblage de quatre couches :

1. **Orchestration de compute éphémère** : Kubernetes Jobs, Tekton, Argo Workflows.
2. **Moteur de build** : BuildKit, Buildah, Kaniko, Dagger, Earthly, Shipwright/Buildpacks.
3. **Transfert de contexte / cache** : CDC (desync/casync), cache NVMe local, S3/MinIO,
   cache registry, P2P Dragonfly, Bazel REAPI, sccache, Gradle cache.
4. **IDE web et accès distant** : code-server, OpenVSCode Server, Coder, Okteto, Gitpod/Ona.

Le control plane à écrire est la couche transverse : auth, API, quotas, catalogue d'images,
orchestration, logs temps réel, artefacts et proxy bastion.

### A.2 Comparatif synthétique

| Outil / projet | Catégorie | Apport | Limite vs notre besoin | Verdict |
|---|---|---|---|---|
| Kubernetes Jobs | Orchestration | Pods éphémères, TTL, quotas, RBAC natif | Pas d'API user, ni logs/artefacts/bastion | **Adopter** comme primitive d'exécution |
| Tekton Pipelines | CI/CD K8s | Tasks/Pipelines, pods isolés, catalogue, Chains, Results | Multi-tenant non clé en main, dashboard basique, pas d'auth user ni bastion | **Composer** plus tard si pipelines |
| Argo Workflows | Workflow K8s | DAG natif, UI riche, artefacts, TTL/GC, archive logs | Moteur généraliste, pas de multi-tenant clé en main, lourd pour un job unique | **S'inspirer**, pas en v1 |
| BuildKit | Moteur d'images OCI | Rootless, cache avancé, multi-stage, utilisé par Tekton/Dagger/Depot/Gitpod | Conçu pour produire des images ; pas nécessaire pour un simple build d'artefacts | **Adopter** uniquement pour l'export OCI |
| Buildah | Moteur de build | Daemonless, rootless, OCI natif | Cache local nécessitant du stockage persistant, moins adapté aux pods éphémères | **Secours** |
| Kaniko | Moteur de build | Daemonless, cache registry, K8s natif | Archivé en juin 2025 ; fork Chainguard maintenance seule | **Éviter** pour du neuf |
| Shipwright | Framework build K8s | API Build/BuildRun, stratégies BuildKit/Buildah/Buildpacks/Kaniko/ko | Orienté image, pas de commande/artefacts arbitraires ; dépend de Tekton | **Non retenu v1** |
| Buildpacks / kpack | Source-to-image | Détection de langage, images reproductibles | Opinionné, pas d'outillage arbitraire | **Non retenu** |
| Dagger | CI programmable | Pipelines en SDK, moteur BuildKit, cache | Ajoute un DSL/SDK, pas un service multi-tenant | **Option avancée** |
| Earthly | Build reproductible | Earthfile, BuildKit, cache | DSL supplémentaire, positionnement de l'éditeur à surveiller | **Non retenu v1** |
| Bazel + REAPI (Buildbarn, BuildGrid, Buildfarm, NativeLink) | Exécution/cache distante | Parallélisme massif, cache d'actions, monorepos | Spécifique Bazel, exploitation complexe | **Profil spécialisé ultérieur** |
| bazel-remote / sccache / Gradle cache | Caches distants | Réutilisation des sorties par langage | Ne remplace pas l'orchestration | **Complément via cache** |
| desync / casync | Transfert de contexte | CDC, déduplication, delta, chunks zstd adressés par hash, stores S3/OCI/local | Outillage à intégrer côté CLI et init container | **Adopter** pour le non-Git / les gros contextes |
| Dragonfly | Distribution P2P | P2P fichiers/images, preheat, réduction d'egress, CNCF graduated 2026 | Composants supplémentaires (scheduler/seed) ; utile à l'échelle | **Option à l'échelle** |
| BuildKit cache S3 (`type=s3`) | Cache de build | Cache froid durable sur S3/MinIO, rehydratation rapide | Backend encore expérimental ; à valider | **Adopter** avec repli registry |
| Spegel / Nydus | Distribution d'images | P2P registry léger / chargement paresseux de couches | Spécifique images, pas contexte | **Option** pour les images de base |
| NVMe local (DaemonSet) / GKE Data Cache | Cache chaud | Latence disque local, hit quasi instantané | Best-effort, perdu à la perte du nœud | **Adopter** comme hot tier |
| DevSpace | Dev K8s client | Build/deploy/hot reload, terminal, port-forward | Client-only, nécessite kubeconfig, pas multi-tenant | **Non retenu** |
| Skaffold | Dev K8s client | Build/push/deploy, file sync | Client-only ; dépôt annoncé archivé au 29/01/2027 | **Éviter** |
| Tilt | Dev K8s local | Live update, UI | Boucle locale, pas de service | **Non retenu** |
| Garden | Dev/prod K8s | Graphe de build/deploy, cache, environnements | Orienté dev workflow, pas API de build | **Non retenu** |
| Okteto | Environnements dev | Environnements éphémères, build service, sync | Plateforme de dev, pas un build API arbitraire | **À considérer si achat** |
| Coder | Workspaces dev | code-server, proxy, RBAC, audit, multi-tenant | Centré workspaces persistants, plus lourd | **S'inspirer / achat possible** |
| Gitpod / Ona | Environnements dev | Environnements éphémères, IDE | Rachat annoncé par la presse spécialisée (août 2026), dépendance incertaine | **Éviter** |
| code-server / OpenVSCode Server | IDE web | VS Code dans le navigateur, auth/proxy intégrés (code-server) | À intégrer au bastion | **Adopter** |
| Depot / Namespace / Blacksmith / BuildJet / WarpBuild | Runners managés | Compute CI éphémère, cache, rapidité | Liés à GitHub Actions ou à leur plateforme | **Option achat**, pas le produit |
| Kueue | File d'attente K8s | Quotas, fair-sharing, multi-tenant | Brique de scheduling, pas de service user | **À considérer** |
| Karpenter | Autoscaling nœuds | Provisionnement spot, scale-to-zero | Ne gère pas les jobs | **Adopter** |

### A.3 Ce qu'on réutilise vs ce qu'on écrit

**Réutiliser**
- Kubernetes Jobs pour l'exécution éphémère et les TTL.
- BuildKit rootless pour l'export OCI optionnel.
- code-server (ou OpenVSCode Server) pour l'IDE web.
- Karpenter pour le scale-to-zero et le spot.
- Kueue si la file d'attente multi-tenant devient nécessaire.
- desync/casync pour le CDC et le transfert delta du contexte.
- Cache NVMe local + BuildKit cache S3 + caches de langage (sccache, Gradle, etc.).
- Dragonfly si le fan-out multi-nœuds ou les gros volumes le justifient.
- S3/MinIO, PostgreSQL, registry OCI.

**Écrire (le vrai périmètre)**
- API REST, auth token/OIDC, tenants et RBAC.
- Job Manager : file, résolution d'image, quotas, timeouts.
- Log Proxy temps réel (SSE/NDJSON) + archivage.
- Artifact Broker (URLs présignées).
- Context Service (Git/CDC, manifeste, chunks) et Cache Manager (tiering, GC).
- Catalogue d'images de base par tenant.
- Proxy bastion IDE authentifié (port-forward + WebSocket).
- GC, métriques, audit.

**Ne pas écrire**
- Un moteur de pipeline (Tekton/Argo le font).
- Un moteur de build (BuildKit le fait).
- Un IDE web (code-server le fait).
- Un autoscaler de nœuds (Karpenter le fait).
- Un protocole de transfert delta ou un cache distribué (desync/S3/Dragonfly le font).

### A.4 Pourquoi pas Tekton ou Argo en v1

Le besoin v1 est un **job unique** : télécharger un contexte, exécuter une commande,
éventuellement exporter une image, streamer les logs. Un `Job` Kubernetes suffit.
Tekton et Argo ajoutent des CRDs, des contrôleurs et un modèle de pipeline qui ne
résolvent pas les vrais problèmes du produit : auth sans kubeconfig, quotas par tenant,
catalogue d'images, bastion IDE, API utilisateur.

Ils deviennent pertinents si l'on ajoute plus tard :
- des pipelines multi-étapes (lint → test → build → scan) ;
- du fan-out / DAG ;
- de la provenance SLSA (Tekton Chains) ;
- un historique de runs requêtable (Tekton Results).

**Décision** : garder une abstraction interne `Build` suffisamment propre pour changer
de backend d'exécution (Job → Tekton/Argo) sans toucher à l'API.

### A.5 Cas Bazel : ne pas traiter Bazel comme un build générique

Un build Bazel dans un pod par job est le pire des deux mondes : on paie le démarrage
du pod et on perd le cache d'actions distribué. Pour les utilisateurs Bazel, la bonne
brique est le **Remote Execution API** :
- cache d'actions + CAS via `bazel-remote`, Buildbarn, BuildGrid, Buildfarm ou NativeLink ;
- exécution distante via Buildbarn/BuildGrid/Buildfarm si nécessaire ;
- le pod ne sert alors qu'à lancer le client Bazel.

C'est un **profil spécialisé** à ajouter après le MVP, pas le socle générique.
Pour les autres langages : `sccache` (Rust/C/C++), cache Gradle, cache Go, stores npm/pnpm.

### A.6 IDE web : code-server plutôt que VS Code Web brut

- **code-server** : auth optionnelle, proxy de dev intégré, marketplace Open VSX
  configurable, télémétrie configurable, endpoint `/healthz`, restrictions de
  téléchargement/upload. Plus adapté à un hébergement multi-tenant.
- **VS Code Web** (`code serve-web`) : pas d'auth intégrée, marketplace Microsoft/CDN,
  télémétrie peu configurable, routage par chemin fragile.
- **Coder** : solution complète (workspaces, proxy, RBAC, audit) ; à considérer si l'IDE
  devient un produit à part entière, sinon surdimensionné.
- **Gitpod/Ona** : racheté par OpenAI en août 2026 ; éviter comme dépendance structurante.

**Décision** : sidecar `code-server`, proxy bastion maison, image épinglée, marketplace
désactivé par défaut.

### A.7 Build vs buy (runners managés)

Les offres Depot, Namespace, Blacksmith, BuildJet, WarpBuild et Latchkey résolvent bien
le compute CI éphémère, mais :
- elles sont souvent liées à GitHub Actions ou à leur propre plateforme ;
- elles ne fournissent pas l'API, le catalogue d'images par user, le bastion IDE
  ni l'intégration à votre registry interne ;
- elles ne permettent pas de choisir finement l'image de base et les outils.

Elles restent une **option de repli** si le besoin se limite à « exécuter des jobs CI
plus vite et moins cher ». Pour le produit décrit ici, la couche d'intégration reste
nécessaire ; autant la construire mince et réutiliser le reste.

### A.8 Sources principales (consultées en novembre 2026)

- Tekton — documentation et releases LTS 2026 : https://tekton.dev/
- Tekton Dashboard, logs : https://tekton.dev/docs/dashboard/logs
- Argo Workflows — documentation v4.1 (2026) : https://argo-workflows.readthedocs.io/
- BuildKit — dépôt et releases (v0.33.1, sept. 2026) : https://github.com/moby/buildkit
- Kaniko — archivage juin 2025 et fork Chainguard : https://github.com/chainguard-forks/kaniko
- Shipwright — v0.20 (juil. 2026) : https://shipwright.io/
- Bazel Remote Execution API : https://github.com/bazelbuild/remote-apis
- Buildbarn : https://github.com/buildbarn/bb-remote-execution
- DevSpace — revue sept. 2026 : https://www.devspace.sh/
- Skaffold — documentation (archivage annoncé au 29/01/2027) : https://skaffold.dev/docs/
- Coder — code-server vs VS Code Web : https://coder.com/docs/user-guides/workspace-access/code-server
- Okteto — environnements de dev : https://www.okteto.com/docs/
- Depot — runners éphémères : https://depot.dev/docs/github-actions/overview
- Blacksmith — runners : https://docs.blacksmith.sh/blacksmith-runners/overview
- Kueue — intégration Tekton (TEP-0164) : https://kueue.sigs.k8s.io/
- desync — transfert delta et CDC : https://github.com/folbricht/desync
- casync — format de chunks content-defined : https://github.com/systemd/casync
- Dragonfly — distribution P2P (CNCF) : https://dragonflyoss.io/
- BuildKit — cache S3 : https://docs.docker.com/build/cache/backends/s3/
- GKE — Local SSD / Data Cache : https://cloud.google.com/kubernetes-engine/docs/concepts/local-ssd

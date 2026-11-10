package kube

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func makeContext(t *testing.T, headers []*tar.Header, bodies map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, hdr := range headers {
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if body, ok := bodies[hdr.Name]; ok {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestValidateContextTarSafe(t *testing.T) {
	data := makeContext(t, []*tar.Header{
		{Name: "CMakeLists.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5},
		{Name: "src/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "src/main.cpp", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "src/main.cpp", Mode: 0o777},
	}, map[string]string{"CMakeLists.txt": "cmake", "src/main.cpp": "main"})
	if err := validateContextTar(bytes.NewReader(data)); err != nil {
		t.Fatalf("validateContextTar() = %v", err)
	}
}

func TestValidateContextTarRejectsUnsafe(t *testing.T) {
	cases := []struct {
		name   string
		header *tar.Header
	}{
		{"parent", &tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}},
		{"absolute", &tar.Header{Name: "/etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}},
		{"symlink escape", &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd", Mode: 0o777}},
		{"absolute symlink", &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777}},
		{"device", &tar.Header{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o644}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := makeContext(t, []*tar.Header{tc.header}, nil)
			if err := validateContextTar(bytes.NewReader(data)); err == nil {
				t.Fatalf("validateContextTar() accepted unsafe header %+v", tc.header)
			}
		})
	}
}

func TestValidateContextTarEmpty(t *testing.T) {
	data := makeContext(t, nil, nil)
	if err := validateContextTar(bytes.NewReader(data)); err == nil {
		t.Fatal("validateContextTar() accepted empty context")
	}
}

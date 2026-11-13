package release

import (
	"ael/internal/store"
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "directory/", "ael"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "client.tar.gz")
			file, _ := os.Create(path)
			compressed := gzip.NewWriter(file)
			archive := tar.NewWriter(compressed)
			header := &tar.Header{Name: name, Mode: 0755, Typeflag: tar.TypeSymlink, Linkname: "outside", Size: 0}
			archive.WriteHeader(header)
			archive.Close()
			compressed.Close()
			file.Close()
			if _, err := ExtractClientPackage(path, t.TempDir()); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}

func TestClientPackageHashAndProtocol(t *testing.T) {
	root := t.TempDir()
	metadata := ClientPackage{Version: "1.2.3", Build: "fixture", Commit: strings.Repeat("a", 40), Kind: "client", Platform: "darwin-arm64", Protocols: Protocols{1, 1, 1}}
	for name, content := range map[string]string{"ael": "binary fixture", "THIRD_PARTY_NOTICES.txt": "notice", "README.md": "readme"} {
		os.WriteFile(filepath.Join(root, name), []byte(content), 0755)
		metadata.Files = append(metadata.Files, PackageFile{Path: name, SHA256: store.Hash([]byte(content)), Size: int64(len(content))})
	}
	write := func() { raw, _ := json.Marshal(metadata); os.WriteFile(filepath.Join(root, "package.json"), raw, 0644) }
	write()
	if _, _, err := ReadClientPackage(root); err != nil {
		t.Fatal(err)
	}
	metadata.Protocols.Skill = 2
	write()
	if _, _, err := ReadClientPackage(root); err == nil {
		t.Fatal("accepted incompatible protocol")
	}
	metadata.Protocols.Skill = 1
	write()
	os.WriteFile(filepath.Join(root, "ael"), []byte("changed"), 0755)
	if _, _, err := ReadClientPackage(root); err == nil {
		t.Fatal("accepted tamper")
	}
}

func TestClientArchiveVerifiesFooterAndRejectsTrailingPayload(t *testing.T) {
	contents := map[string][]byte{"ael": []byte("fixture executable"), "THIRD_PARTY_NOTICES.txt": []byte("fixture notice"), "README.md": []byte("fixture readme")}
	metadata := ClientPackage{Version: "1.2.3", Build: "fixture", Commit: strings.Repeat("a", 40), Kind: "client", Platform: "darwin-arm64", Protocols: Protocols{1, 1, 1}}
	for name, body := range contents {
		metadata.Files = append(metadata.Files, PackageFile{Path: name, SHA256: store.Hash(body), Size: int64(len(body))})
	}
	contents["package.json"], _ = json.Marshal(metadata)
	path := filepath.Join(t.TempDir(), "client.tar.gz")
	file, _ := os.Create(path)
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for name, body := range contents {
		if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	archive.Close()
	compressed.Close()
	file.Close()
	if _, err := ExtractClientPackage(path, t.TempDir()); err != nil {
		t.Fatal("valid archive rejected", err)
	}
	original, _ := os.ReadFile(path)
	corrupt := append([]byte{}, original...)
	corrupt[len(corrupt)-8] ^= 0xff
	os.WriteFile(path, corrupt, 0644)
	if _, err := ExtractClientPackage(path, t.TempDir()); err == nil {
		t.Fatal("accepted corrupted gzip checksum")
	}
	os.WriteFile(path, original, 0644)
	extra, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	second := gzip.NewWriter(extra)
	second.Write([]byte("unselected trailing content"))
	second.Close()
	extra.Close()
	if _, err := ExtractClientPackage(path, t.TempDir()); err == nil {
		t.Fatal("accepted trailing compressed payload")
	}
}

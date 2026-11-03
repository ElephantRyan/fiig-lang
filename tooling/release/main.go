// Command release builds the alpha's downloadable archives: the static
// `fiig` binary for each platform, with the README, license, the
// getting-started, deploying, and known-limitations guides, and the
// getting-started example, so an archive runs that guide with nothing else
// installed. It writes a .zip for Windows, a .tar.gz otherwise, and
// SHA256SUMS. Only the standard library, so it runs the same on a release
// runner and a Windows desktop.
//
// Usage, from the repository root:
//
//	go run ./tooling/release -version v0.1.0-alpha.1 [-out dist] [-targets linux/amd64,windows/amd64]
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// targets are the platforms an alpha ships for.
var targets = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"}

// included is what every archive carries beside the binary, relative to the
// repository root.
var included = []string{
	"README.md",
	"LICENSE",
	"docs/getting-started.md",
	"docs/deploying.md",
	"docs/known-limitations.md",
	"examples/getting-started",
}

func main() {
	version := flag.String("version", "", "the release version, e.g. v0.1.0-alpha.1 (required)")
	out := flag.String("out", "dist", "directory to write archives into")
	only := flag.String("targets", "", "comma-separated GOOS/GOARCH list (default: every release target)")
	flag.Parse()
	if *version == "" {
		fmt.Fprintln(os.Stderr, "usage: release -version VERSION [-out DIR] [-targets LIST]")
		os.Exit(2)
	}
	want := targets
	if *only != "" {
		want = strings.Split(*only, ",")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	var archives []string
	for _, t := range want {
		name, err := build(*version, t, *out)
		if err != nil {
			fail(fmt.Errorf("%s: %w", t, err))
		}
		fmt.Println(name)
		archives = append(archives, name)
	}
	if err := writeSums(*out, archives); err != nil {
		fail(err)
	}
}

// build compiles fiig for target and packs it into an archive in out,
// returning the archive's file name.
func build(version, target, out string) (string, error) {
	goos, goarch, ok := strings.Cut(target, "/")
	if !ok {
		return "", fmt.Errorf("target must be GOOS/GOARCH")
	}
	exe := "fiig"
	if goos == "windows" {
		exe += ".exe"
	}
	stage, err := os.MkdirTemp("", "fiig-release-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	bin := filepath.Join(stage, exe)
	absBin, err := filepath.Abs(bin)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-s -w -X fiig-lang/compiler/version.Version="+version,
		"-o", absBin, ".")
	cmd.Dir = filepath.Join("cmd", "fiig")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Every archive unpacks into one directory named for itself.
	base := fmt.Sprintf("fiig-%s-%s-%s", version, goos, goarch)
	files := map[string]string{base + "/" + exe: bin}
	for _, p := range included {
		if err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			files[base+"/"+filepath.ToSlash(path)] = path
			return nil
		}); err != nil {
			return "", err
		}
	}
	if goos == "windows" {
		return base + ".zip", writeZip(filepath.Join(out, base+".zip"), files)
	}
	return base + ".tar.gz", writeTarGz(filepath.Join(out, base+".tar.gz"), files, base+"/"+exe)
}

func sortedKeys(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func writeZip(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, name := range sortedKeys(files) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		if err := copyFile(w, files[name]); err != nil {
			return err
		}
	}
	return zw.Close()
}

// writeTarGz writes files, marking exe executable: the mode a Windows
// file system can't record.
func writeTarGz(path string, files map[string]string, exe string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		info, err := os.Stat(files[name])
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if name == exe {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if err := copyFile(tw, files[name]); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyFile(w io.Writer, path string) error {
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(w, r)
	return err
}

// writeSums writes SHA256SUMS in the format `sha256sum -c` reads.
func writeSums(out string, archives []string) error {
	var b strings.Builder
	for _, a := range archives {
		data, err := os.ReadFile(filepath.Join(out, a))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), a)
	}
	return os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(b.String()), 0o644)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "release:", err)
	os.Exit(1)
}

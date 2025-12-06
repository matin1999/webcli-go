package edit

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func HandleSnapshot(ansRoot string, args []string, user string) {

	if len(args) < 1 {
		fmt.Printf(`
snapshot create                 - create a tar.gz snapshot of the ansible project (saved under ansible_root/snapshots)
	    Example: snapshot create
snapshot list                   - list available snapshots
	    Example: snapshot list
snapshot restore <snapshot>     - restore the project from a snapshot (destructive: replaces current files)
		Example: snapshot restore snapshot-20250918T123456Z.tar.gz
		`)
		return
	}
	switch args[0] {
	case "create":
		f, err := createSnapshot(ansRoot)
		if err != nil {
			fmt.Println("ERR:", err)
		} else {
			fmt.Println("Created snapshot:", f)
		}
	case "list":
		list, err := listSnapshots(ansRoot)
		if err != nil {
			fmt.Println("ERR:", err)
			break
		}
		for _, n := range list {
			fmt.Println("-", n)
		}
	case "restore":
		if len(args) != 2 {
			fmt.Println("Usage: snapshot restore <snapshot-file>")
			break
		}
		if err := restoreSnapshot(ansRoot, args[1]); err != nil {
			fmt.Println("ERR:", err)
		} else {
			fmt.Println("Snapshot restored:", args[1])
		}
	default:
		fmt.Println("Usage: snapshot create | list | restore <file>")
	}
}

func snapshotsDir(ansRoot string) string {
	return filepath.Join(ansRoot, "snapshots")
}

func createSnapshot(ansRoot string) (string, error) {
	snapDir := snapshotsDir(ansRoot)
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir snapshots: %w", err)
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	base := fmt.Sprintf("snapshot-%s.tar.gz", ts)
	outPath := filepath.Join(snapDir, base)

	outF, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create snapshot file: %w", err)
	}
	defer func() { _ = outF.Close() }()

	gw := gzip.NewWriter(outF)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	err = filepath.Walk(ansRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == snapDir {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(ansRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, f); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
		return nil
	})

	if err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("walk error: %w", err)
	}
	return outPath, nil
}

func listSnapshots(ansRoot string) ([]string, error) {
	snapDir := snapshotsDir(ansRoot)
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "snapshot-") && strings.HasSuffix(name, ".tar.gz") {
			out = append(out, name)
		}
	}
	return out, nil
}

func restoreSnapshot(ansRoot, snapshotFile string) error {
	var snapPath string
	if filepath.IsAbs(snapshotFile) {
		snapPath = snapshotFile
	} else {
		snapPath = filepath.Join(snapshotsDir(ansRoot), snapshotFile)
	}

	if _, err := os.Stat(snapPath); err != nil {
		return fmt.Errorf("snapshot not found: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "ansible-restore-")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	if err := extractTarGz(snapPath, tmpDir); err != nil {
		return fmt.Errorf("extract snapshot: %w", err)
	}

	snapDir := snapshotsDir(ansRoot)
	children, err := os.ReadDir(ansRoot)
	if err != nil {
		return fmt.Errorf("read ansRoot: %w", err)
	}
	for _, c := range children {
		childPath := filepath.Join(ansRoot, c.Name())
		if samePath(childPath, snapDir) {
			continue
		}
		if err := os.RemoveAll(childPath); err != nil {
			return fmt.Errorf("remove existing content %s: %w", childPath, err)
		}
	}

	tmpChildren, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("read tmpDir: %w", err)
	}
	for _, c := range tmpChildren {
		src := filepath.Join(tmpDir, c.Name())
		dst := filepath.Join(ansRoot, c.Name())
		if err := os.Rename(src, dst); err == nil {
			continue
		}
		if err := copyAll(src, dst); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
		}
	}
	return nil
}

func samePath(a, b string) bool {
	ra, errA := filepath.Abs(a)
	rb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

func extractTarGz(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if strings.Contains(hdr.Name, "..") || filepath.IsAbs(hdr.Name) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, hdr.FileInfo().Mode()); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outF, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(outF, tr); err != nil {
				_ = outF.Close()
				return err
			}
			_ = outF.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func copyAll(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyAll(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	// file copy
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

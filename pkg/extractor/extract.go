package extract

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// extractTarGz extracts tar.gz at srcFile into destDir
func extractTarGz(srcFile, destDir string) error {
	f, err := os.Open(srcFile)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		targetPath := filepath.Join(destDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
			// preserve mod time
			os.Chtimes(targetPath, time.Now(), hdr.ModTime)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			// remove if exists
			os.Remove(targetPath)
			if err := os.Symlink(hdr.Linkname, targetPath); err != nil {
				return err
			}
		default:
			// skip other types for simplicity
		}
	}
	return nil
}

// copyDirContents copies all files from srcDir to dstDir, overwriting existing files
func copyDirContents(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// if symlink
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			os.Remove(dstPath)
			return os.Symlink(target, dstPath)
		}

		// regular file
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()

		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return nil
	})
}

func createTarGz(srcDir, destFile string) error {
	out, err := os.Create(destFile)
	if err != nil {
		return fmt.Errorf("create dest file: %w", err)
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	gw.Name = filepath.Base(srcDir) + ".tar.gz"
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// walk directory
	err = filepath.Walk(srcDir, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create header
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(filepath.Dir(srcDir), file)
		if err != nil {
			return err
		}
		// Use forward slashes in tar headers
		header.Name = filepath.ToSlash(relPath)

		// If file is a symlink, capture the link target
		if fi.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(file)
			if err != nil {
				return err
			}
			header.Linkname = linkTarget
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// For directories and symlinks, no file body is needed
		if fi.Mode().IsRegular() {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}
	return nil
}

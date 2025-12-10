package pcap

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/matin1999/webcli-go/internal/configs"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	Dir          string
	MaxSeconds   int
	DefaultMaxMB int
}

// Run handles:
//
//  pcap <iface> <seconds 1..MaxSeconds> [maxMB 1..200] [filter...]   (local)
//  pcap ls
//  pcap download <file>
//  pcap delete [file]            (if file omitted → delete all)
//  pcap hosts                    (list inventory [all] hosts)
//  pcap remote <host> <iface> <seconds> [filter...]  (remote, saved locally)
func Run(args []string, user string, cfg Options) error {
	if len(args) == 0 || args[0] == "help" {
		fmt.Printf(`
pcap hosts                     - list inventory [all] hosts (allowed targets)
pcap <host> <iface> <seconds> [filter...] - capture on <host>, save locally
pcap ls                        - list available pcap files
pcap download <file>           - print a downloadable URL for <file>
pcap delete [file]             - delete a specific pcap file (or all if omitted)
`, cfg.MaxSeconds)
		return nil
	}

	switch args[0] {
	case "ls":
		return list(cfg.Dir)

	case "download":
		if len(args) < 2 {
			return fmt.Errorf("usage: pcap download <file>")
		}
		fmt.Printf("Download: %s\n", buildDownloadURL(args[1]))
		return nil

	case "delete":
		if len(args) < 2 {
			return deleteAll(cfg.Dir)
		}
		return deletePcap(cfg.Dir, args[1])

	case "hosts":
		hosts, err := inventoryAllHosts()
		if err != nil {
			return err
		}
		if len(hosts) == 0 {
			fmt.Println("No hosts found in inventory [all].")
			return nil
		}
		for _, h := range hosts {
			fmt.Println(h)
		}
		return nil

	case "example":
		return printexamples()

	default:
		return remoteCapture(args, user, cfg)
	}
}

func list(dir string) error {
	_ = os.MkdirAll(dir, 0o755)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list pcaps: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("(no pcaps yet)")
		return nil
	}
	fmt.Println("PCAP files:")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fmt.Printf(" - %s\n", e.Name())
	}
	return nil
}

func capture(args []string, user string,cfg Options) error {
	// minimal validation + usage
	if len(args) < 2 {
		return fmt.Errorf("usage: pcap <iface> <seconds 1..%d> [maxMB 1..200] [filter...]", cfg.MaxSeconds)
	}

	iface := args[0]
	secs, err := strconv.Atoi(args[1])
	if err != nil || secs < 1 || secs > cfg.MaxSeconds {
		return fmt.Errorf("seconds must be 1..%d", cfg.MaxSeconds)
	}

	// optional maxMB
	maxMB := cfg.DefaultMaxMB
	off := 2
	if len(args) >= 3 {
		if cand := args[2]; cand != "" && len(cand) <= 3 {
			if n, e := strconv.Atoi(cand); e == nil && n >= 1 && n <= 200 {
				maxMB = n
				off = 3
			}
		}
	}

	filter := ""
	if len(args) > off {
		filter = strings.Join(args[off:], " ")
	}

	// ensure output dir exists
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return fmt.Errorf("mkdir pcap dir: %w", err)
	}

	// filenames
	ts := time.Now().UTC().Format("20060102T150405Z")
	tmpName := fmt.Sprintf("%s-%s.pcap.tmp", iface, ts)
	finalName := fmt.Sprintf("%s-%s.pcap", iface, ts)
	tmpPath := filepath.Join(cfg.Dir, tmpName)
	finalPath := filepath.Join(cfg.Dir, finalName)

	// build tcpdump args
	tdArgs := []string{"-i", iface, "-w", tmpPath, "-U"}
	if maxMB > 0 {
		tdArgs = append(tdArgs, "-C", strconv.Itoa(maxMB))
	}
	if filter != "" {
		tdArgs = append(tdArgs, strings.Fields(filter)...)
	}

	// find tcpdump
	tcpdumpBin, err := exec.LookPath("tcpdump")
	if err != nil {
		// try common locations before failing
		if _, statErr := os.Stat("/usr/bin/tcpdump"); statErr == nil {
			tcpdumpBin = "/usr/bin/tcpdump"
		} else if _, statErr2 := os.Stat("/bin/tcpdump"); statErr2 == nil {
			tcpdumpBin = "/bin/tcpdump"
		} else {
			return fmt.Errorf("tcpdump not found in PATH; install tcpdump")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs+3)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, tcpdumpBin, tdArgs...)
	cmd.Env = append(os.Environ(), "LANG=C")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start tcpdump: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil && ctx.Err() != context.DeadlineExceeded {
			return fmt.Errorf("tcpdump exit error: %w", err)
		}
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			time.Sleep(300 * time.Millisecond)
			_ = cmd.Process.Kill()
		}
		<-done
	}

	if _, err := os.Stat(tmpPath); err == nil {
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return fmt.Errorf("capture finished but rename failed: %w", err)
		}
		fmt.Println("OK:", filepath.Base(finalPath))
		fmt.Printf("Download Link:\n %s", buildDownloadURL(finalName))
		return nil
	}

	files, _ := filepath.Glob(filepath.Join(cfg.Dir, iface+"-*"))
	if len(files) > 0 {
		latest := files[len(files)-1]
		fmt.Println("OK:", filepath.Base(latest))
		fmt.Printf("Download Link:\n %s", buildDownloadURL(filepath.Base(latest)))
		return nil
	}

	return fmt.Errorf("capture finished but no pcap found")
}

func inventoryAllHosts() ([]string, error) {
	invPath := filepath.Join(
		strings.TrimPrefix(configs.InventoryKey, "/"),
	)
	b, err := os.ReadFile(invPath)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}

	var hosts []string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	inAll := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// group header
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			grp := strings.ToLower(strings.Trim(line, "[] \t"))
			inAll = (grp == "all")
			continue
		}
		if !inAll {
			continue
		}
		// host line (may include vars); take first field
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		host := fields[0]
		if !configs.HostRe.MatchString(host) {
			continue
		}
		hosts = append(hosts, host)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan inventory: %w", err)
	}
	return hosts, nil
}

func hostAllowed(host string) bool {
	if !configs.HostRe.MatchString(host) {
		return false
	}
	hosts, err := inventoryAllHosts()
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if h == host {
			return true
		}
	}
	return false
}

func remoteCapture(args []string, user string, cfg Options) error {
	// usage: pcap remote <host> <iface> <seconds> [filter...]
	if len(args) < 3 {
		return fmt.Errorf("usage: pcap remote <host> <iface> <seconds> [filter...]")
	}
	host := args[0]
	iface := args[1]
	secs, err := strconv.Atoi(args[2])
	if err != nil || secs < 1 || secs > cfg.MaxSeconds {
		return fmt.Errorf("seconds must be 1..%d", cfg.MaxSeconds)
	}
	if !hostAllowed(host) {
		return fmt.Errorf("host %q not in inventory [all]", host)
	}

	filter := ""
	if len(args) > 3 {
		filter = strings.Join(args[3:], " ")
	}

	// ensure local output dir exists
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return fmt.Errorf("mkdir pcap dir: %w", err)
	}

	// local filename
	ts := time.Now().UTC().Format("20060102T150405Z")
	finalName := fmt.Sprintf("%s-%s-%s.pcap", host, iface, ts)
	finalPath := filepath.Join(cfg.Dir, finalName)

	sshUser := configs.DefaultUser
	sshKey := configs.DefaultPrivKey

	// Remote command (NO sudo): stream pcap to stdout; timeout ends tcpdump after <secs>
	remote := []string{
		"timeout", strconv.Itoa(secs),
		"tcpdump", "-i", iface, "-U", "-w", "-",
		// "-s", "0", // uncomment if you want full-size packets (no snaplen truncation)
	}
	if strings.TrimSpace(filter) != "" {
		remote = append(remote, filter)
	}

	sshArgs := []string{
		"-i", sshKey,
		"-o", "StrictHostKeyChecking=no",
		fmt.Sprintf("%s@%s", sshUser, host),
		strings.Join(remote, " "),
	}

	outFile, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("create local pcap: %w", err)
	}
	defer outFile.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs+15)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr

	runErr := cmd.Run()

	// flush file to disk
	_ = outFile.Sync()

	// Treat remote timeout(1) exit code 124 as success
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok && ee.ExitCode() == 124 {
			runErr = nil
		}
	}

	// If there is still an error, but we have data, keep the file and warn
	if runErr != nil {
		if fi, statErr := os.Stat(finalPath); statErr == nil && fi.Size() > 24 {
			fmt.Println("WARN: remote exited with error but capture saved:", filepath.Base(finalPath))
			fmt.Printf("Download Link:\n %s", buildDownloadURL(finalName))
			return nil
		}
		_ = os.Remove(finalPath)
		return fmt.Errorf("ssh/tcpdump failed: %w", runErr)
	}

	fmt.Println("OK:", filepath.Base(finalPath))
	fmt.Printf("Download Link:\n %s", buildDownloadURL(finalName))
	return nil
}

func buildDownloadURL(file string) string {
	host := os.Getenv("HOST_ADDRESS")
	return fmt.Sprintf("https://%s/pcaps/%s", host, file)
}

func deletePcap(dir string, file string) error {
	if file == "" {
		return deleteAll(dir)
	}
	if err := os.Remove(filepath.Join(dir, file)); err != nil {
		return fmt.Errorf("ERR: failed to delete %s/%s: %v", dir, file, err)
	}
	fmt.Printf("Deleted pcap file: %s\n", file)
	return nil
}

func deleteAll(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list for delete: %w", err)
	}
	deleted := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			deleted++
		}
	}
	fmt.Printf("Deleted %d pcap file(s)\n", deleted)
	return nil
}

func printexamples() error {
	fmt.Printf(`
pcap remote 127.0.0.1 eth0 45 tcp port 443

`)
	return nil
}

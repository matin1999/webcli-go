package ansible

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/matin1999/webcli-go/internal/configs"
	"github.com/matin1999/webcli-go/internal/helpers"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apenella/go-ansible/pkg/execute"
	"github.com/apenella/go-ansible/pkg/options"
	"github.com/apenella/go-ansible/pkg/playbook"
	stdoutcb "github.com/apenella/go-ansible/pkg/stdoutcallback"
	cbresults "github.com/apenella/go-ansible/pkg/stdoutcallback/results"
)

type Config struct {
	PlaybookPath   string
	InventoryPath  string
	ExtraVars      map[string]interface{}
	ExtraVarsFiles []string
	Become         bool
	BecomeMethod   string
	BecomeUser     string
	Verbose        bool
	CheckMode      bool
	DiffMode       bool
	Tags           []string
	SkipTags       []string
	Limit          string
	Connection     string
	PrivateKey     string
	User           string
	ExtraArgs      []string
	Timeout        time.Duration
}

type Result struct {
	Success  bool
	Duration time.Duration
	Error    error
}

type Runner struct {
	verbose bool
	stdout  io.Writer
	stderr  io.Writer
}

func HandleAnsible(args []string, r *Runner) {
	if len(args) < 1 {
		fmt.Printf(`
service ls                       				- getting running service
service install   <service-name>              	- deploy service (no tags)
service uninstall <playbook-file-name>              	- remove service (tags=absent)
`)
		return
	}

	subcmd := strings.TrimSpace(args[0])

	if subcmd == "ls" {
		ctx := context.Background()
		if err := listDockerServices(ctx, r); err != nil {
			fmt.Println("ERR:", err)
		}
		return
	}

	if subcmd != "install" && subcmd != "uninstall" {
		fmt.Fprintln(r.stderr, "ERR: expected one of: ls | install | uninstall")
		return
	}
	if len(args) < 2 {
		fmt.Fprintln(r.stderr, "ERR: missing <service-name>")
		return
	}

	playBookKey := strings.TrimSpace(args[1])

	// Remaining args (k=v pairs)
	restArgs := make([]string, 0, len(args)-2)
	for i := 2; i < len(args); i++ {
		a := strings.TrimSpace(args[i])
		if a == "" {
			continue
		}
		if a == "--tags" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			next := strings.TrimSpace(args[i+1])
			i++
			restArgs = append(restArgs, a+"="+next)
			continue
		}
		restArgs = append(restArgs, a)
	}

	kv, leftover := helpers.ParseArgs(restArgs)
	if len(leftover) > 0 {
		fmt.Fprintln(r.stderr, "ERR: unexpected positional args:", strings.Join(leftover, " "))
		return
	}

	norm := map[string]string{}
	for k, v := range kv {
		k2 := strings.TrimLeft(k, "-")
		switch k2 {
		case "skip-tags", "skip_tags":
			k2 = "skip"
		case "verbose":
		}
		norm[k2] = v
	}
	kv = norm

	switch subcmd {
	case "install":
		delete(kv, "tags")
	case "uninstall":
		kv["tags"] = "absent"
	}

	ctx := context.Background()
	if to, err := helpers.ParseTimeoutSeconds(kv["timeout"], 30*time.Minute); err == nil && to > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}

	extraKVs := map[string]interface{}{}

	res, err := runAnsible(ctx, r, playBookKey, kv, extraKVs)
	if err != nil {
		fmt.Fprintln(r.stderr, "ERR:", err)
		return
	}
	fmt.Fprintf(r.stdout, "OK: success=%v duration=%s\n", res.Success, res.Duration)
}

func New(verbose bool, out io.Writer, errw io.Writer) *Runner {
	if out == nil {
		out = os.Stdout
	}
	if errw == nil {
		errw = os.Stderr
	}
	return &Runner{verbose: verbose, stdout: out, stderr: errw}
}

func (r *Runner) runPlaybook(ctx context.Context, cfg *Config) (*Result, error) {
	start := time.Now()
	res := &Result{}

	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	pbOpts := &playbook.AnsiblePlaybookOptions{
		Inventory: cfg.InventoryPath,
		ExtraVars: cfg.ExtraVars,
		Check:     cfg.CheckMode,
		Diff:      cfg.DiffMode,
		Limit:     cfg.Limit,
	}

	if len(cfg.Tags) > 0 {
		pbOpts.Tags = strings.Join(cfg.Tags, ",")
	}
	if len(cfg.SkipTags) > 0 {
		pbOpts.SkipTags = strings.Join(cfg.SkipTags, ",")
	}
	if len(cfg.ExtraVarsFiles) > 0 {
		pbOpts.ExtraVarsFile = cfg.ExtraVarsFiles
	}

	connOpts := &options.AnsibleConnectionOptions{
		Connection: cfg.Connection,
		PrivateKey: cfg.PrivateKey,
		User:       cfg.User,
	}

	privesc := &options.AnsiblePrivilegeEscalationOptions{
		Become:       cfg.Become,
		BecomeMethod: cfg.BecomeMethod,
		BecomeUser:   cfg.BecomeUser,
	}

	exec := execute.NewDefaultExecute(
		execute.WithTransformers(cbresults.LogFormat(cbresults.DefaultLogFormatLayout, func(s string) string { return s })),
		execute.WithWrite(r.stdout),
	)

	cmd := &playbook.AnsiblePlaybookCmd{
		Playbooks:                  []string{cfg.PlaybookPath},
		Options:                    pbOpts,
		ConnectionOptions:          connOpts,
		PrivilegeEscalationOptions: privesc,
		Exec:                       exec,
		StdoutCallback:             stdoutcb.DefaultStdoutCallback,
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	err := cmd.Run(runCtx)
	res.Duration = time.Since(start)
	if err != nil {
		res.Success = false
		res.Error = err
		return res, fmt.Errorf("playbook execution failed: %w", err)
	}

	res.Success = true
	return res, nil
}

func runAnsible(ctx context.Context, r *Runner, playbookKey string, kv map[string]string, extraFromE map[string]interface{}) (*Result, error) {

	// join root + file safely
	playbookPath := filepath.Join(configs.DefaultAnsibleConfigRootPath, playbookKey)

	limit := strings.TrimSpace(kv["limit"])
	if limit != "" && !configs.LimitRe.MatchString(limit) {
		return nil, fmt.Errorf("invalid limit")
	}
	tags := strings.TrimSpace(kv["tags"])
	if tags != "" && !configs.TagsRe.MatchString(tags) {
		return nil, fmt.Errorf("invalid tags")
	}
	skip := strings.TrimSpace(kv["skip"])
	if skip != "" && !configs.TagsRe.MatchString(skip) {
		return nil, fmt.Errorf("invalid skip")
	}

	to, err := helpers.ParseTimeoutSeconds(strings.TrimSpace(kv["timeout"]), 30*time.Minute)
	if err != nil {
		return nil, err
	}

	extra := map[string]interface{}{}
	for k, v := range kv {
		if configs.AllowedExtra[k] {
			extra[k] = v
		}
	}
	for k, v := range extraFromE {
		extra[k] = v
	}

	if p := strings.TrimSpace(kv["pass"]); p != "" && strings.ToLower(p) != "prompt" {
		extra["ansible_password"] = p
	}
	if bp := strings.TrimSpace(kv["become_pass"]); bp != "" && strings.ToLower(bp) != "prompt" {
		extra["ansible_become_password"] = bp
	}

	user := configs.DefaultUser
	if v := strings.TrimSpace(kv["user"]); v != "" {
		user = v
	}

	inventoryPath := filepath.Join(configs.DefaultAnsibleConfigRootPath, configs.InventoryKey)
	extraVarsFile := filepath.Join(configs.DefaultAnsibleConfigRootPath, configs.DefaultExtraVarsFile)

	cfg := &Config{
		PlaybookPath:   playbookPath,
		InventoryPath:  inventoryPath,
		ExtraVars:      extra,
		ExtraVarsFiles: []string{"@" + extraVarsFile},
		Connection:     "ssh",
		User:           user,
		PrivateKey:     configs.DefaultPrivKey,
		Become:         true,
		BecomeMethod:   "sudo",
		BecomeUser:     "root",
		Verbose:        false,
		Tags:           helpers.SplitCSV(tags),
		SkipTags:       helpers.SplitCSV(skip),

		Timeout: to,
	}

	return r.runPlaybook(ctx, cfg)
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if strings.TrimSpace(cfg.PlaybookPath) == "" {
		return fmt.Errorf("playbook path is required")
	}
	if strings.TrimSpace(cfg.InventoryPath) == "" {
		return fmt.Errorf("inventory is required (file/dir or 'host,')")
	}
	if _, err := os.Stat(cfg.PlaybookPath); err != nil {
		return fmt.Errorf("playbook file check: %w", err)
	}
	if !(strings.HasSuffix(cfg.InventoryPath, ",") && !strings.ContainsAny(cfg.InventoryPath, "/\\")) {
		if _, err := os.Stat(cfg.InventoryPath); err != nil {
			return fmt.Errorf("inventory check: %w", err)
		}
	}
	return nil
}

func listDockerServices(ctx context.Context, r *Runner) error {
	inventoryPath := filepath.Join(configs.DefaultAnsibleConfigRootPath, configs.InventoryKey)
	host := firstHostFromInventory(inventoryPath)
	if host == "" {
		host = "all"
	}

	if r.stdout != nil {
		fmt.Fprintln(r.stdout, "=== Swarm Services ===")
		fmt.Fprintln(r.stdout, "NAME\tMODE\tREPLICAS\tIMAGE")
	}

	serviceFormat := "{% raw %}{{.Name}}\\t{{.Mode}}\\t{{.Replicas}}\\t{{.Image}}{% endraw %}"
	cmd := exec.CommandContext(
		ctx,
		"ansible", host,
		"-i", inventoryPath,
		"-m", "shell",
		"-a", "docker service ls --format '"+serviceFormat+"'",
		"-b", "--become-user", "root",
		"-u", configs.DefaultUser,
		"--private-key", configs.DefaultPrivKey,
		"-o",
	)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return err
	}
	pretty := extractAdhocStdout(string(out))
	if err := printServicesTable(r.stdout, pretty); err != nil {
		return err
	}

	if r.stdout != nil {
		fmt.Fprintln(r.stdout, "\n=== Running Containers ===")
		fmt.Fprintln(r.stdout, "CONTAINER IMAGE\tSTATUS\tNAMES")
	}

	psFormat := "{% raw %}\\t{{.Image}}\\t{{.Status}}\\t{{.Names}}{% endraw %}"
	cmd2 := exec.CommandContext(
		ctx,
		"ansible", host,
		"-i", inventoryPath,
		"-m", "shell",
		"-a", "docker ps --format '"+psFormat+"'",
		"-b", "--become-user", "root",
		"-u", configs.DefaultUser,
		"--private-key", configs.DefaultPrivKey,
		"-o",
	)

	out2, err := cmd2.CombinedOutput()
	if err != nil && len(out2) == 0 {
		return err
	}
	pretty2 := extractAdhocStdout(string(out2))

	// tabwriter table
	tw := tabwriter.NewWriter(r.stdout, 0, 8, 2, ' ', 0)
	for _, line := range strings.Split(pretty2, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintln(tw, line)
	}
	return tw.Flush()
}

func firstHostFromInventory(invPath string) string {
	fi, err := os.Stat(invPath)
	if err != nil || fi.IsDir() {
		return ""
	}
	f, err := os.Open(invPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func extractAdhocStdout(s string) string {
	if i := strings.Index(s, "(stdout)"); i >= 0 {
		t := strings.TrimSpace(s[i+len("(stdout)"):])
		t = strings.ReplaceAll(t, `\n`, "\n")
		t = strings.ReplaceAll(t, `\t`, "\t")
		return strings.TrimSpace(t)
	}
	if i := strings.Index(s, ">>"); i >= 0 {
		t := s[i+2:]
		return strings.TrimLeft(t, " \t\r\n")
	}
	return strings.TrimSpace(s)
}

func printServicesTable(w io.Writer, content string) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMODE\tREPLICAS\tIMAGE")
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "\t") {
			fmt.Fprintln(tw, line)
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				fields[0], fields[1], fields[2], strings.Join(fields[3:], " "))
		}
	}
	return tw.Flush()
}

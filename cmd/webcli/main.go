package main

import (
	"bufio"
	"fmt"
	"os"
	"github.com/matin1999/webcli-go/internal/configs"
	"github.com/matin1999/webcli-go/internal/helpers"
	"github.com/matin1999/webcli-go/pkg/ansible"
	"github.com/matin1999/webcli-go/pkg/edit"
	"github.com/matin1999/webcli-go/pkg/pcap"
	"strings"
)


func main() {
	user := helpers.GettingUsername()
	if user == "" {
		user = "(unknown)"
	}
	fmt.Printf("Restricted Webcli . User=%s\nType 'help' for options.\n\n", user)

	r := ansible.New(true, os.Stdout, os.Stderr)

	ansRoot := configs.DefaultAnsibleConfigRootPath

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !in.Scan() {
			return
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		fs := strings.Fields(line)
		cmd, args := fs[0], fs[1:]

		switch cmd {
		case "help":
			helpers.Help()
		case "exit", "quit":
			return
		case "date":
			_ = helpers.RunLocal("/bin/date")
		case "service":
			ansible.HandleAnsible(args, r)
		case "df":
			_ = helpers.RunLocal("/bin/df", "-h")
		case "ping":
			helpers.HandlePing(args)
		case "pcap":
			cfg := pcap.Options{
				Dir:          "/app/pcaps",
				MaxSeconds:   120,
				DefaultMaxMB: 25,
			}
			if err := pcap.Run(args, user, cfg); err != nil {
				fmt.Println("ERR:", err)
			}
		case "snapshot":
			edit.HandleSnapshot(ansRoot,args,user)

		default:
			fmt.Println("ERR: command not allowed (type 'help')")
		}
	}
}

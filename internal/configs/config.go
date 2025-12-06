package configs

import (
	"regexp"
)

var (
	NumRe  = regexp.MustCompile(`^[0-9]{1,3}$`)
	HostRe = regexp.MustCompile(`^[a-zA-Z0-9.-]{1,64}$`)

	LimitRe = regexp.MustCompile(`^[A-Za-z0-9._,-]{1,128}$`)
	TagsRe  = regexp.MustCompile(`^[A-Za-z0-9_,.-]{1,128}$`)

	BoolTrue = map[string]bool{"1": true, "true": true, "yes": true, "on": true}

    InventoryKey   = "/inventories/inv_113"
    DefaultExtraVarsFile  = "vars.yml"

	AllowedExtra = map[string]bool{
		"env":     true,
		"version": true,
	}
	DefaultAnsibleConfigRootPath = "/root/ansible"

	DefaultInventoryKey = "default"
	DefaultUser         = "root"
	DefaultPrivKey      = "/root/.ssh/id_rsa"
)

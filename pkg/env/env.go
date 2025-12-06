package env

import (
	"os"
	"strconv"
)

type Envs struct {
	CaptchaRetryCount      int
	CaptchaBlockTime       int
	CaptchaRetryTimeWindow int
	AbsSessionHours        int
	IdleTimeoutMinutes     int
	GottyPortInternal      string
	AdminUsers             string
	WebcliBin              string
	GatewayPort            string
	DebugMode              bool
}

func ReadEnvs() *Envs {
	envs := Envs{}
	var err error

	envs.GottyPortInternal = os.Getenv("GOTTY_PORT_INTERNAL")
	envs.AdminUsers = os.Getenv("ADMIN_USERS")
	envs.WebcliBin = os.Getenv("WEBCLI_BIN")
	envs.GatewayPort = os.Getenv("GATEWAY_PORT")

	envs.CaptchaRetryCount, err = strconv.Atoi(os.Getenv("CAPTCHA_RETRY_COUNT"))
	if err != nil {
		envs.CaptchaRetryCount = 3
	}

	envs.CaptchaBlockTime, err = strconv.Atoi(os.Getenv("CAPTCHA_BLOCK_TIME"))
	if err != nil {
		envs.CaptchaBlockTime = 30
	}

	envs.CaptchaRetryTimeWindow, err = strconv.Atoi(os.Getenv("CAPTCHA_RETRY_TIME_WINDOW"))
	if err != nil {
		envs.CaptchaRetryTimeWindow = 30
	}

	envs.AbsSessionHours, err = strconv.Atoi(os.Getenv("ABS_SESSION_HOURS"))
	if err != nil {
		envs.AbsSessionHours = 24
	}

	envs.IdleTimeoutMinutes, err = strconv.Atoi(os.Getenv("IDLE_TIMEOUT_MINUTES"))
	if err != nil {
		envs.IdleTimeoutMinutes = 15
	}

	return &envs
}

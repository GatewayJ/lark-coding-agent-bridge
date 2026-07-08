package service

import (
	"fmt"
	"strings"
)

type DefinitionInputs struct {
	Executable string
	EnvPath    string
	Profile    string
	Home       string
	StdoutPath string
	StderrPath string
}

func BuildLaunchdPlist(inputs DefinitionInputs) (string, error) {
	label, err := LaunchAgentLabel(inputs.Profile)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>run</string>
        <string>--profile</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>%s</string>
        <key>LARK_CHANNEL_HOME</key>
        <string>%s</string>
    </dict>
</dict>
</plist>
`, xmlEscape(label), xmlEscape(inputs.Executable), xmlEscape(inputs.Profile), xmlEscape(inputs.StdoutPath), xmlEscape(inputs.StderrPath), xmlEscape(inputs.EnvPath), xmlEscape(inputs.Home)), nil
}

func BuildSystemdUnit(inputs DefinitionInputs) (string, error) {
	return fmt.Sprintf(`[Unit]
Description=Lark Channel Bridge bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart="%s" run --profile "%s"
Restart=always
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s
Environment="PATH=%s"
Environment="LARK_CHANNEL_HOME=%s"

[Install]
WantedBy=default.target
`, systemdEscape(inputs.Executable), systemdEscape(inputs.Profile), inputs.StdoutPath, inputs.StderrPath, systemdEscape(inputs.EnvPath), systemdEscape(inputs.Home)), nil
}

func BuildWindowsLauncherCmd(inputs DefinitionInputs) (string, error) {
	return strings.Join([]string{
		"@echo off",
		"setlocal DisableDelayedExpansion",
		fmt.Sprintf(`set "LARK_CHANNEL_HOME=%s"`, windowsBatchEscape(inputs.Home)),
		fmt.Sprintf(`set "PATH=%s"`, windowsBatchEscape(inputs.EnvPath)),
		fmt.Sprintf(`"%s" run --profile "%s" >> "%s" 2>> "%s"`, windowsBatchEscape(inputs.Executable), windowsBatchEscape(inputs.Profile), windowsBatchEscape(inputs.StdoutPath), windowsBatchEscape(inputs.StderrPath)),
		"",
	}, "\r\n"), nil
}

func xmlEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	return value
}

func systemdEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func windowsBatchEscape(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '%':
			out.WriteString("%%")
		case '^':
			out.WriteString("^^")
		case '&', '|', '<', '>', '(', ')':
			out.WriteByte('^')
			out.WriteRune(r)
		case '"':
			out.WriteString(`^"`)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

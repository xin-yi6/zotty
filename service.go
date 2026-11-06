package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"text/template"
	"time"
)

const serviceLabel = "io.github.xin-yi6.zotty.receiver"

// The KeepAlive setting restarts the receiver after a crash but lets
// `zotty service uninstall` stop it for good.
var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>{{.Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{.Log}}</string>
</dict>
</plist>
`))

func renderPlist(binary, logPath string) ([]byte, error) {
	var out bytes.Buffer
	err := plistTemplate.Execute(&out, map[string]string{
		"Label": serviceLabel, "Binary": binary, "Log": logPath,
	})
	return out.Bytes(), err
}

func plistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}

func launchTarget() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func runService(args []string) error {
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	p := addPathFlags(fs)
	words := parseArgs(fs, args)
	if len(words) != 1 {
		return errors.New("usage: zotty service install|uninstall|status")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch words[0] {
	case "install":
		return installService(p, home)
	case "uninstall":
		return uninstallService(home)
	case "status":
		return exec.Command("launchctl", "print", launchTarget()+"/"+serviceLabel).Run()
	}
	return fmt.Errorf("unknown service command %q", words[0])
}

// installService copies this binary somewhere stable, so rebuilding zotty or
// moving the project can't break the login item, then starts it.
func installService(p *paths, home string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	binary := filepath.Join(p.data, "bin", "zotty")
	if err := copyExecutable(self, binary); err != nil {
		return err
	}
	plist, err := renderPlist(binary, filepath.Join(home, "Library", "Logs", "zotty.log"))
	if err != nil {
		return err
	}
	path := plistPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := stopService(); err != nil {
		return err
	}
	if err := os.WriteFile(path, plist, 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("launchctl", "bootstrap", launchTarget(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("start the receiver: %v: %s", err, out)
	}
	fmt.Println("The receiver is running and will start at each login")
	return nil
}

// stopService unloads a running receiver and waits until launchd has let it
// go. bootout returns before that, and loading the service again too soon
// fails with an input/output error.
func stopService() error {
	service := launchTarget() + "/" + serviceLabel
	if exec.Command("launchctl", "print", service).Run() != nil {
		return nil
	}
	exec.Command("launchctl", "bootout", service).Run()
	for range 50 {
		if exec.Command("launchctl", "print", service).Run() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("the running receiver didn't stop. Try again in a moment")
}

func uninstallService(home string) error {
	if err := stopService(); err != nil {
		return err
	}
	if err := os.Remove(plistPath(home)); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Println("The receiver is stopped and won't start at login")
	return nil
}

// copyExecutable replaces the destination by rename, which works even while the
// old binary is running.
func copyExecutable(from, to string) error {
	if from == to {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(to), "zotty-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}

// Package nginxboot renders this container's nginx.conf from
// nginx.conf.template and starts nginx as a background daemon - the same
// two steps client/start.sh used to perform via envsubst, now done in Go
// since the Go binary is the container's entrypoint (it must run
// afterwards, to serve the control API and gate the chisel tunnel on
// enrollment).
package nginxboot

import (
	"os"
	"os/exec"
	"text/template"
)

const (
	TemplatePath = "/etc/nginx/nginx.conf.template"
	ConfPath     = "/etc/nginx/nginx.conf"
)

type templateData struct {
	AllowedHostPattern string
	LocalIngress       string
}

// Render fills in nginx.conf.template with this appliance's local ingress
// settings and writes the result to ConfPath.
func Render(allowedHostPattern string, localIngress string) error {
	tmplBytes, err := os.ReadFile(TemplatePath)
	if err != nil {
		return err
	}
	tmpl, err := template.New("nginx").Parse(string(tmplBytes))
	if err != nil {
		return err
	}
	out, err := os.Create(ConfPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return tmpl.Execute(out, templateData{AllowedHostPattern: allowedHostPattern, LocalIngress: localIngress})
}

// Start launches nginx. nginx daemonizes itself by default (forks and the
// invoking process exits once the master process is up), so this returns
// once nginx is genuinely running in the background - it does not block for
// nginx's lifetime.
func Start() error {
	return exec.Command("nginx").Run()
}

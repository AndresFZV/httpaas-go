package web

import "testing"

func TestDisplayPath(t *testing.T) {
	tests := []struct{ path, home, want string }{
		{`C:\Users\felip\.ssh\httpaas`, `C:\Users\felip`, "~/.ssh/httpaas"},
		{`c:\users\felip\Desarrollo\data\known_hosts`, `C:\Users\felip`, "~/Desarrollo/data/known_hosts"},
		{`D:\otra\llave`, `C:\Users\felip`, "D:/otra/llave"},
		{"/home/a/.ssh/k", "/home/a/", "~/.ssh/k"},
		{"/home/ab/k", "/home/a", "/home/ab/k"},
		{"/home/a/k", "", "/home/a/k"},
	}
	for _, tt := range tests {
		if got := displayPath(tt.path, tt.home); got != tt.want {
			t.Errorf("displayPath(%q, %q) = %q, se esperaba %q", tt.path, tt.home, got, tt.want)
		}
	}
}

func TestSSHPrefix(t *testing.T) {
	v := newAccessView(AccessConfig{
		SSHUser:        "deploy",
		SSHKey:         `C:\Users\felip\.ssh\httpaas`,
		KnownHostsFile: `C:\Users\felip\Mis proyectos\httpaas-go\data\known_hosts`,
		TemplateDisk:   `C:\HTTPaaS\ApacheServer.vdi`,
		Home:           `C:\Users\felip`,
	})

	want := `ssh -i ~/.ssh/httpaas -o "UserKnownHostsFile=~/Mis proyectos/httpaas-go/data/known_hosts" deploy@`
	if v.SSHPrefix != want {
		t.Errorf("SSHPrefix = %q, se esperaba %q", v.SSHPrefix, want)
	}
	if v.TemplateDisk != "ApacheServer.vdi" {
		t.Errorf("TemplateDisk = %q", v.TemplateDisk)
	}
}

func TestSSHPrefixWithoutKey(t *testing.T) {
	if v := newAccessView(AccessConfig{SSHUser: "deploy"}); v.SSHPrefix != "" {
		t.Errorf("sin llave, SSHPrefix debe quedar vacío; es %q", v.SSHPrefix)
	}
}

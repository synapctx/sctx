package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestZZ(t *testing.T) {
	for _, c := range []struct{ f, argv string }{
		{"apt-get-install", "apt-get install -y curl jq"}, {"apt-get-update", "apt-get update"}, {"apt-install", "apt install -y htop"},
		{"apt-list-upgradable", "apt list --upgradable"}, {"dnf-install", "dnf install -y htop"}, {"dnf-upgrade", "dnf upgrade -y"},
		{"yum-install", "yum install -y jq"}, {"yum-remove", "yum remove -y jq"}, {"apk-add", "apk add curl jq"}, {"apk-upgrade", "apk upgrade"},
		{"pacman-S", "pacman -S --noconfirm htop"}, {"pacman-Syu", "pacman -Syu --noconfirm"}, {"apt-get-install-notfound", "apt-get install -y x"},
		{"apk-add-notfound", "apk add x"}, {"dnf-install-notfound", "dnf install -y x"}, {"dnf-check-update", "dnf check-update"}, {"yum-check-update", "yum check-update"},
		{"apt-remove", "apt remove -y htop"}, {"apt-list-installed-head", "apt list --installed"},
	} {
		raw, _ := os.ReadFile("testdata/" + c.f + ".txt")
		argv := strings.Fields(c.argv)
		var f format.Formatter
		for _, x := range All() {
			if x.Descriptor().Command == argv[0] {
				f = x
			}
		}
		out, err := f.Aggressive(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader(string(raw)), ExitCode: 0})
		fmt.Printf("##### %s raw=%d err=%v out=%d\n%s\n", c.f, len(raw), err, len(out.Body), out.Body)
	}
}

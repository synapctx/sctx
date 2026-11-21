package hook

import (
	"strings"
	"testing"
)

// wave3RewriteCases pins the rows and the streaming/interactive refusals of the
// third coverage wave: one declined spelling per rule plus the nearby finite
// spellings that must keep their coverage. Appended to TestRewrite's table (and
// so to FuzzRewrite's seeds).
func wave3RewriteCases() []rewriteTestCase {
	var cases []rewriteTestCase
	wrap := func(cmd string) { cases = append(cases, rewriteTestCase{"wrap " + cmd, cmd, "sctx " + cmd, true}) }
	keep := func(cmd string) { cases = append(cases, rewriteTestCase{"decline " + cmd, cmd, cmd, false}) }

	// Follow flags hidden in a short cluster used to slip past the exact match.
	keep("tail -fn 100 x")
	keep("tail -Fn5 x")
	keep("tail --follow=name x")
	keep("journalctl -fu nginx")
	keep("journalctl --follow -u x")
	wrap("tail -n 100 x")
	wrap("journalctl -u nginx -n 5 --no-pager")

	// An inner program that never finishes is refused behind a launcher too.
	keep("ssh host tail -f x")
	keep("ssh host top")
	keep("docker exec c top")
	keep("docker run --rm alpine top")
	keep("docker run --rm alpine")
	keep("docker run -it alpine sh")
	keep("docker run -d alpine ls")
	keep("docker compose run web ls")
	wrap("ssh host ls")
	wrap("docker exec c ls")
	wrap("docker run --rm alpine ls")
	wrap("podman run --rm alpine ls")
	wrap("docker compose run -T web ls") // -T suffices: attached non-TTY stdin is the agent's own
	wrap("docker compose run -T --interactive=false web ls")
	wrap("kubectl config view")
	wrap("brew update")
	wrap("brew reinstall wget")

	// Scanners.
	wrap("trivy image alpine:3")
	wrap("trivy fs .")
	wrap("gitleaks detect")
	wrap("kube-linter lint .")
	wrap("osv-scanner scan .")
	wrap("semgrep --config auto .")
	wrap("checkov -d .")
	wrap("tflint")
	wrap("tfsec .")
	wrap("grype alpine:3")
	wrap("ansible-lint")
	wrap("helmfile diff")
	keep("trivy server")
	keep("trivy image --listen :4954 x")
	keep("trivy plugin install x")
	keep("semgrep lsp")
	keep("semgrep mcp")
	keep("semgrep login")
	keep("tflint --langserver")
	keep("gitleaks stdin")
	keep("gitleaks detect --pipe")
	keep("helmfile status")

	// Transfer and archive tools.
	wrap("wget https://example.com/x.tar.gz")
	wrap("tar xzf a.tgz")
	wrap("gtar czf a.tgz d")
	wrap("bsdtar -tf a.tar")
	wrap("zip -r a.zip d")
	wrap("unzip -o a.zip")
	wrap("unzip -l a.zip")
	wrap("unzip -qo a.zip -d out")
	keep("unzip a.zip")
	keep("unzip a.zip -d out")
	keep("unzip -q a.zip -dout")
	wrap("unzip -q a.zip -d out -o")
	wrap("unzip -Z a.zip")

	// openssl: only the rendered shapes, and s_client only with a visible stdin.
	wrap("openssl x509 -in a.pem -text -noout")
	wrap("openssl crl -in a.crl -text")
	wrap("openssl req -in a.csr -text -noout")
	wrap("openssl req -new -newkey rsa:2048 -nodes -subj /CN=x -keyout k.pem")
	wrap("openssl s_client -connect h:443 </dev/null")
	wrap("openssl s_client -connect h:443 < /dev/null")
	wrap("openssl s_client -connect h:443 <<< ''")
	wrap("openssl s_client -connect h:443 < req.txt")
	keep("openssl s_client -connect h:443")
	keep("echo | openssl s_client -connect h:443")
	keep("openssl s_server -accept 4433 -cert c.pem")
	keep("openssl s_time -connect h:443")
	keep("openssl req -new -newkey rsa:2048 -nodes -keyout k.pem")
	keep("openssl req -x509 -newkey rsa:2048 -keyout k.pem -out c.pem")
	keep("openssl rand -hex 8")
	keep("openssl enc -aes-256-cbc -in a")

	// svn.
	wrap("svn update")
	wrap("svn up")
	wrap("svn checkout https://x/repo")
	wrap("svn commit -m 'msg'")
	wrap("svn ci -F msg.txt")
	wrap("svn commit --message msg")
	keep("svn commit")
	keep("svn ci")
	keep("svn import . https://x/repo")
	keep("svn log")
	keep("glab mr list")

	// sbt: the interactive shell, continuous mode, consoles and runs.
	wrap("sbt test")
	wrap("sbt compile")
	wrap(`sbt "testOnly a.B"`)
	wrap("sbt -Dsbt.offline=true clean test")
	wrap("sbt -mem 2048 test")
	keep("sbt")
	keep("sbt -Dsbt.offline=true")
	keep("sbt -mem 2048")
	keep("sbt ~test")
	keep(`sbt "~testQuick"`)
	keep(`sbt "clean; ~compile"`)
	keep("sbt run")
	keep(`sbt "runMain a.Main"`)
	keep("sbt console")
	keep("sbt consoleQuick")
	keep("sbt shell")
	keep("sbt --client test")

	// Other language toolchains.
	wrap("mix test")
	wrap("mix compile")
	wrap("mix deps.get")
	wrap("dart analyze")
	wrap("dart test")
	wrap("dart pub get")
	wrap("dart pub publish --dry-run")
	wrap("dart pub publish --force")
	wrap("zig build")
	wrap("zig build test")
	wrap("zig test src/main.zig")
	wrap("rebar3 compile")
	wrap("rebar3 eunit")
	wrap("rebar3 as test ct")
	wrap("cabal build")
	wrap("cabal test")
	wrap("ghc Main.hs")
	wrap("prove -r t")
	wrap("bazel build //...")
	wrap("bazelisk test //pkg:all")
	wrap("nix build .#app")
	wrap("nix flake check")
	wrap("nix-build")
	keep("mix phx.server")
	keep("mix test --listen-on-stdin")
	keep("dart run bin/main.dart")
	keep("dart pub publish")
	keep("dart pub run build_runner watch")
	keep("dart pub global run x")
	keep("dart pub login")
	keep("dart test --pause-after-load")
	keep("zig build run")
	keep("zig build run-server")
	keep("zig build serve")
	keep("zig build --watch")
	keep("zig build -fwatch")
	keep("zig build --webui")
	keep("zig run src/main.zig")
	keep("rebar3 shell")
	keep("rebar3 as test shell")
	keep("rebar3 auto")
	keep("cabal run")
	keep("cabal repl")
	keep("cabal exec ghc")
	keep("cabal v2-run x")
	keep("ghc --interactive")
	keep("ghc -e main")
	keep("bazel run //app")
	keep("bazelisk run //app")
	keep("nix develop")
	keep("nix shell nixpkgs#hello")
	keep("nix run nixpkgs#hello")
	keep("nix repl")
	keep("nix-shell")
	keep("nix-shell -p hello")
	keep("nix-shell --run bash")
	keep(`nix-shell --run "make; bash"`)
	keep(`nix-shell --run "tail -f x"`)
	keep(`nix-shell -p x --run "make | tee log"`)
	keep(`nix-shell --run "mysql"`)
	wrap(`nix-shell --run "make"`)
	wrap(`nix-shell -p go --run "go test ./..."`)
	wrap(`nix-shell --command "pytest"`)

	// Database clients and migration tools: a REPL is never wrapped.
	wrap(`mysql -e "select 1"`)
	wrap(`mysql -u root -p -e "select 1"`)
	wrap(`mysql -Nse "select 1"`)
	wrap(`mysql -Be 'select 1'`)
	wrap("mysql --execute='select 1'")
	wrap("mysql --version")
	keep("mysql")
	keep("mysql -u root db")
	keep("mysql -psecure")
	keep("mysql -uroot -psecure db")
	wrap(`mongosh --eval "db.stats()"`)
	wrap(`mongosh "mongodb://h/db" --eval 'db.a.find()'`)
	wrap("mongosh --file x.js")
	wrap("mongosh mongodb://h/db x.js")
	keep("mongosh")
	keep(`mongosh "mongodb://h/db"`)
	keep("mongosh --shell x.js")
	keep(`mongosh --eval "1" --shell`)
	wrap(`sqlite3 db.sqlite "select 1"`)
	wrap(`sqlite3 -json db.sqlite "select 1"`)
	wrap(`sqlite3 -cmd ".mode box" db.sqlite "select 1"`)
	wrap("sqlite3 db.sqlite .tables")
	wrap("sqlite3 -version")
	keep("sqlite3")
	keep("sqlite3 db.sqlite")
	keep("sqlite3 -json db.sqlite")
	keep(`sqlite3 -cmd "select 1" db.sqlite`)
	keep(`sqlite3 -interactive db.sqlite "select 1"`)
	wrap("redis-cli get k")
	wrap("redis-cli -h r -p 6380 info")
	wrap("redis-cli --bigkeys")
	wrap("redis-cli --scan --pattern 'a*'")
	keep("redis-cli")
	keep("redis-cli -h r")
	keep("redis-cli monitor")
	keep("redis-cli subscribe ch")
	keep("redis-cli --stat")
	keep("redis-cli --latency")
	keep("redis-cli -r -1 ping")
	wrap(`clickhouse-client -q "select 1"`)
	wrap(`clickhouse-client --query "select 1"`)
	keep("clickhouse-client")
	keep("clickhouse-client -h h")
	wrap("dbt run")
	wrap("dbt test --select x")
	wrap("dbt build")
	keep("dbt docs serve")
	keep("dbt init")
	wrap("flyway migrate")
	wrap("flyway info")
	wrap("liquibase update")
	wrap("liquibase status")
	keep("liquibase init start-h2")
	keep("liquibase init project")

	// Linux system administration.
	wrap("apt-get install -y curl")
	wrap("apt-get -qy install curl")
	wrap("apt-get -y install curl")
	wrap("apt-get -o Dpkg::Options::=--force-confold -y install curl")
	wrap("dnf -y upgrade")
	keep("dnf -x kernel upgrade")
	keep("apt-get -o APT::Get::Assume-Yes=true")
	wrap("apt install -y htop")
	wrap("apt-get update")
	wrap("apt update")
	wrap("apt list --upgradable")
	wrap("apt-get -s install x")
	wrap("apt-get install --dry-run x")
	wrap("apt-get -o APT::Get::Assume-Yes=true install x")
	wrap("dnf install -y htop")
	wrap("dnf check-update")
	wrap("dnf makecache")
	wrap("yum -y install jq")
	wrap("apk add curl")
	wrap("pacman -Ss htop")
	wrap("pacman -Qi htop")
	wrap("pacman -Syu --noconfirm")
	keep("apt-get install curl")
	keep("apt install htop")
	keep("apt remove nginx")
	keep("apt-get upgrade")
	keep("apt-get -o Dpkg::Options::=--force-confold install x")
	keep("apt-get -oDebug::pkgProblemResolver=y install x")
	keep("dnf install htop")
	keep("dnf upgrade")
	keep("yum install jq")
	keep("pacman -S htop")
	keep("pacman -Syu")
	keep("pacman -R htop")
	keep("top")
	keep("top -b")
	keep("top -d 1")
	wrap("top -bn1")
	wrap("top -b -n 1")
	keep("htop")
	keep("mtr 1.1.1.1")
	wrap("dmesg")
	wrap("dmesg -T")
	keep("dmesg -w")
	keep("dmesg --follow")
	keep("dmesg -TW")
	wrap("ss -tulpn")
	keep("ss -c")
	keep("ss --continuous")
	keep("ss -tulpnc")
	keep("ss -E")
	wrap("netstat -tulpn")
	keep("netstat -c")
	keep("netstat -tulpnc")
	wrap("ip addr")
	wrap("ip link")
	wrap("ip a")
	keep("ip monitor")
	keep("ip -s monitor")
	keep("ip route monitor")
	wrap("iptables -L -n")
	wrap("ip6tables -S")
	wrap("ufw status")
	wrap("lsblk")
	wrap("mount")
	keep("traceroute example.com") // unbounded: minutes on a silent path
	wrap("traceroute -m 5 example.com")
	wrap("systemctl --failed")
	wrap("systemctl status nginx")
	wrap("systemctl list-units")
	keep("systemctl restart nginx")

	return cases
}

// TestLinuxStreams is the direct table for the package-manager prompts and the
// monitors: each row is `program args...` and whether it never reaches EOF.
func TestLinuxStreams(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"apt-get install -y curl", false}, {"apt-get install curl", true}, {"apt install htop", true}, {"apt install -y htop", false}, {"apt-get -qy install x", false}, {"apt-get -yq install x", false},
		{"apt-get update", false}, {"apt update", false}, {"apt list --upgradable", false}, {"apt-get -s install x", false}, {"apt-get install --dry-run x", false}, {"apt-get -o APT::Get::Assume-Yes=true install x", false},
		{"apt-get -o Dpkg::Options::=--force-confold install x", true}, {"apt-get -oDebug::pkgProblemResolver=y install x", true}, {"apt remove nginx", true}, {"apt-get upgrade", true}, {"apt-get dist-upgrade -y", false}, {"apt-get autoremove", true},
		{"dnf install htop", true}, {"dnf install -y htop", false}, {"dnf upgrade", true}, {"dnf check-update", false}, {"dnf makecache", false}, {"yum install jq", true}, {"yum -y install jq", false}, {"dnf install --assumeyes x", false}, {"dnf --assumeno install x", false},
		{"pacman -S htop", true}, {"pacman -S --noconfirm htop", false}, {"pacman -Syu", true}, {"pacman -Syu --noconfirm", false}, {"pacman -Sy", false}, {"pacman -Ss htop", false}, {"pacman -Qi htop", false}, {"pacman -R htop", true}, {"pacman -Rns htop --noconfirm", false}, {"pacman -U x.pkg", true}, {"pacman -Sc", true},
		{"top", true}, {"top -b", true}, {"top -bn1", false}, {"top -b -n 1", false}, {"top -n1 -b", false}, {"top -d 1", true}, {"top -bn 1 -o %MEM", false}, {"top -b -n1 -w 200", false},
		{"htop", true}, {"watch -n1 ls", true}, {"tcpdump -i eth0", true}, {"mtr 1.1.1.1", true}, {"mtr --report 1.1.1.1", false}, {"mtr -rw -c3 1.1.1.1", false},
		{"dmesg", false}, {"dmesg -w", true}, {"dmesg --follow", true}, {"dmesg -T", false}, {"dmesg -TW", true}, {"dmesg --follow-new", true},
		{"vmstat", false}, {"vmstat 1", true}, {"vmstat 1 3", false}, {"iostat", false}, {"iostat 1", true}, {"iostat -x 1 3", false}, {"iostat -xz 2", true}, {"mpstat -P ALL 1", true}, {"mpstat -P ALL 1 1", false}, {"sar 1", true}, {"sar -u 1 5", false}, {"vmstat -s", false},
		{"ss -tulpn", false}, {"ss -c", true}, {"ss --continuous", true}, {"ss -E", true}, {"ss -tulpnc", true}, {"netstat -c", true}, {"netstat -tulpnc", true}, {"netstat -tulpn", false}, {"netstat --continuous", true},
		{"pacman --sync foo", true}, {"pacman --remove foo", true}, {"pacman --upgrade x.pkg", true}, {"pacman --sync --sysupgrade", true}, {"pacman --sync --refresh", false}, {"pacman --sync --search foo", false}, {"pacman --sync --noconfirm foo", false}, {"pacman --query", false},
		{"apk add -i curl", true}, {"apk add --interactive curl", true}, {"apk add curl", false}, {"apk add -it curl", true}, {"apk add -ti curl", false}, {"apk add -t build-deps curl", false},
		{"traceroute host", true}, {"traceroute6 host", true}, {"tracepath host", true}, {"traceroute -m 5 host", false}, {"traceroute -m5 host", false}, {"tracepath -m 5 host", false}, {"traceroute -q 1 -w 1 host", false}, {"traceroute -q 1 host", true}, {"traceroute -n host", true},
		{"ip addr", false}, {"ip monitor", true}, {"ip -br addr", false}, {"ip -s monitor", true}, {"ip -n ns monitor", true},
	} {
		f := strings.Fields(tc.line)
		if got := linuxStreams(f[0], f[1:]); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.line, got, tc.want)
		}
	}
}

// TestMysqlShortClusterHasE: a glued password must never read as `-e`.
func TestMysqlShortClusterHasE(t *testing.T) {
	for arg, want := range map[string]bool{
		"-Nse": true, "-Be": true, "-tBe": true, "-e": false, "-psecure": false, "-pXe": false,
		"-uroot": false, "--execute": false, "-NB": false, "-ue": false,
	} {
		if got := mysqlShortClusterHasE([]string{arg}); got != want {
			t.Errorf("mysqlShortClusterHasE(%q) = %v, want %v", arg, got, want)
		}
	}
}

// TestFlagCluster pins that only an all-letters cluster counts: a glued option
// value that happens to contain the letter is not a consent or answer flag.
func TestFlagCluster(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		letters string
		want    bool
	}{
		{[]string{"-qy"}, "y", true},
		{[]string{"-oDebug=y"}, "y", false},
		{[]string{"--yes"}, "y", false},
		{[]string{"-y"}, "y", true},
		{[]string{"-x"}, "y", false},
	} {
		if got := flagCluster(tc.args, tc.letters); got != tc.want {
			t.Errorf("flagCluster(%v, %q) = %v, want %v", tc.args, tc.letters, got, tc.want)
		}
	}
}

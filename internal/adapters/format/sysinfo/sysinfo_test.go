package sysinfo

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, tier, argv, raw string) (format.Rendered, error) {
	t.Helper()
	args := strings.Fields(argv)
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == args[0] {
			f = x
		}
	}
	in := format.Input{Argv: args, Stdout: strings.NewReader(raw)}
	if tier == "relaxed" {
		return f.Relaxed(context.Background(), in)
	}
	return f.Aggressive(context.Background(), in)
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file, argv string
		keep, drop []string
	}{
		{"top", "top -bn1",
			[]string{"top - 02:32:43 up 3:38, 0 user, load average: 6.66, 7.17, 4.07", "Tasks: 197 total, 1 running, 196 sleeping, 0 stopped, 0 zombie", "%Cpu(s): 42.9 us", "MiB Mem : 23994.5 total",
				"PID USER PR NI VIRT RES SHR S %CPU %MEM TIME+ COMMAND", "1 root 20 0 21232 11916 8968 S 0.0 0.0 0:00.58 systemd", "…+182 more processes (nc ×90, sleep ×90, sh ×1, top ×1)"},
			[]string{"   PID USER", "1364 root"}},
		{"top-o", "top -b -n 1 -o %MEM", []string{"…+182 more processes", "74 systemd+ 20 0 21444 12420 10332 S 0.0 0.1 0:00.03 systemd+"}, nil},
		{"dmesg", "dmesg",
			[]string{"[    3.831145] docker0: port 1(vethff8ae95) entered disabled state ×72 (last [13077.802266])", "[ 7066.685203] hrtimer: interrupt took 2635333 ns",
				"/proc/cgroups lists only v1 controllers", "ICMPv6: NA: 02:00:00:00:00:01 advertised our address fd00:db8::2 on eth0! ×6"},
			[]string{"[   13.", "vethfd302cf: entered allmulticast mode\n"}},
		{"mount-docker", "mount",
			[]string{"/dev/vda1 on /var/lib/docker type ext4 (rw,relatime,discard,noauto_da_alloc)", "type overlay (rw,relatime,lowerdir=…(13 layers),upperdir=…,workdir=…,nouserxattr)", "pseudo/kernel mounts ×36 (type:mountpoint): devtmpfs:/oldroot/dev proc:/oldroot/proc",
				"cgroup2:/sys/fs/cgroup", "/dev/vdb on /oldroot type erofs"},
			[]string{"io.containerd.snapshotter", "upperdir=/"}},
		{"mount", "mount", []string{"/dev/vda1 on /etc/hosts type ext4", "pseudo/kernel mounts ×16"}, []string{"proc on /proc type proc"}},
		{"lsblk", "lsblk", []string{"NAME MAJ:MIN RM SIZE RO TYPE MOUNTPOINTS", "`-vda1 254:1 0 512G 0 part /etc/resolv.conf", "  /etc/hosts", "vdb 254:16 0 539.8M 1 disk", "0B devices without mounts ×16: nbd0-nbd15"}, []string{"nbd1 "}},
		{"lsblk-a", "lsblk -a", []string{"loop devices ×8: loop0 loop1", "ram15 1:15 0 4M 0 disk", "nbd0-nbd15"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := fixture(t, tc.file)
			out, err := run(t, "aggressive", tc.argv, raw)
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q", d)
				}
			}
			if !out.Elided {
				t.Errorf("Elided must be set")
			}
		})
	}
}

// Blocked (D) and zombie (Z) processes are what top is read for; they survive
// even past the row cut.
func TestTopKeepsBlockedAndZombieRowsPastTheCut(t *testing.T) {
	var b strings.Builder
	b.WriteString("top - 10:00:00 up 1 day,  2 users,  load average: 0.10, 0.20, 0.30\nTasks:  40 total,   1 running,  38 sleeping,   0 stopped,   1 zombie\n%Cpu(s):  1.0 us\n\n")
	b.WriteString("    PID USER      PR  NI    VIRT    RES    SHR S  %CPU  %MEM     TIME+ COMMAND\n")
	for i := 1; i <= 38; i++ {
		state := "S"
		cmd := "worker"
		switch i {
		case 30:
			state, cmd = "D", "rsync"
		case 35:
			state, cmd = "Z", "defunct"
		}
		b.WriteString(strings.Repeat(" ", 5) + itoa(1000+i) + " root      20   0  100000  10000   5000 " + state + "   0.0   0.1   0:00.01 " + cmd + "\n")
	}
	out, err := run(t, "aggressive", "top -bn1", b.String())
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"1030 root 20 0 100000 10000 5000 D 0.0 0.1 0:00.01 rsync", "1035 root 20 0 100000 10000 5000 Z 0.0 0.1 0:00.01 defunct", "…+21 more processes (worker ×21)"} {
		if !strings.Contains(body, want) {
			t.Errorf("lost %q in:\n%s", want, body)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// Error-looking dmesg lines fold only when byte-identical: two distinct errors
// that differ in a number must both survive.
func TestDmesgDistinctErrorsAreNotMerged(t *testing.T) {
	raw := strings.Repeat("[   10.000001] usb 1-1: device descriptor read/64, error -71\n", 3) +
		"[   11.000001] usb 1-2: device descriptor read/64, error -110\n" +
		"[   12.000001] EXT4-fs error (device sda1): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0\n" +
		"[   13.000001] EXT4-fs error (device sda2): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0\n" +
		strings.Repeat("[   14.000001] eth0: link up\n", 5)
	out, err := run(t, "aggressive", "dmesg", raw)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"error -71 ×3", "error -110", "device sda1", "device sda2", "eth0: link up ×5"} {
		if !strings.Contains(body, want) {
			t.Errorf("lost %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "error -110 ×") || strings.Contains(body, "sda2): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0 ×") {
		t.Errorf("distinct errors were merged:\n%s", body)
	}
}

func TestDmesgKeepsContinuationAndTraceLines(t *testing.T) {
	raw := strings.Repeat("[    1.000000] eth0: link up\n", 4) + "[    2.000000] Call Trace:\n[    2.000001]  <TASK>\n  not a timestamped line\n"
	out, err := run(t, "aggressive", "dmesg", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "  not a timestamped line") || !strings.Contains(string(out.Body), "Call Trace:") {
		t.Errorf("lost trace lines:\n%s", out.Body)
	}
}

func TestMountOverlayRowCountsLayers(t *testing.T) {
	raw := strings.Repeat("overlay on /var/lib/docker/overlay2/x/merged type overlay (rw,relatime,lowerdir=/var/lib/desktop-containerd/snapshots/1/fs:/var/lib/desktop-containerd/snapshots/2/fs:/var/lib/desktop-containerd/snapshots/3/fs,upperdir=/var/lib/desktop-containerd/snapshots/4/fs,workdir=/var/lib/desktop-containerd/snapshots/4/work)\n", 3)
	out, err := run(t, "aggressive", "mount", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "lowerdir=…(3 layers),upperdir=…,workdir=…") {
		t.Errorf("layers not counted:\n%s", out.Body)
	}
}

func TestDeclines(t *testing.T) {
	top, dmesg, mount, lsblk := fixture(t, "top"), fixture(t, "dmesg"), fixture(t, "mount-docker"), fixture(t, "lsblk")
	for _, tc := range []struct{ argv, raw string }{
		{"top", top}, {"top -b", top}, {"top -d 2", top}, {"top -n1", top}, {"top -u root", top},
		{"dmesg -w", dmesg}, {"dmesg --follow", dmesg}, {"dmesg -W", dmesg}, {"dmesg -J", dmesg}, {"dmesg --json", dmesg}, {"dmesg -c", dmesg}, {"dmesg -C", dmesg}, {"dmesg -r", dmesg},
		{"mount /dev/sda1 /mnt", mount}, {"mount -o remount,rw /", mount}, {"mount -a", mount}, {"mount --bind /a /b", mount},
		{"lsblk -f", lsblk}, {"lsblk -o NAME,SIZE", lsblk}, {"lsblk -J", lsblk}, {"lsblk --json", lsblk}, {"lsblk -P", lsblk}, {"lsblk -r", lsblk}, {"lsblk -fs", lsblk}, {"lsblk -nd", lsblk},
	} {
		t.Run(tc.argv, func(t *testing.T) {
			if _, err := run(t, "aggressive", tc.argv, tc.raw); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
	t.Run("top -bn2 with two header blocks", func(t *testing.T) {
		if _, err := run(t, "aggressive", "top -bn2", top+top); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("relaxed dmesg has nothing lossless to do", func(t *testing.T) {
		if _, err := run(t, "relaxed", "dmesg", dmesg); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
}

func TestErrorTextPassesThrough(t *testing.T) {
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == "lsblk" {
			f = x
		}
	}
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"lsblk", "/dev/nosuch"},
		Stderr: strings.NewReader("lsblk: /dev/nosuch: not a block device\n"), ExitCode: 32})
	if !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("a pure error message must decline, got %v %q", err, out.Body)
	}
}

func TestStderrIsAppended(t *testing.T) {
	var f format.Formatter
	for _, x := range All() {
		if x.Descriptor().Command == "dmesg" {
			f = x
		}
	}
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"dmesg"}, Stdout: strings.NewReader(fixture(t, "dmesg")),
		Stderr: strings.NewReader("dmesg: read kernel buffer failed: Operation not permitted\n")})
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || !strings.HasSuffix(string(out.Body), "Operation not permitted") {
		t.Errorf("stderr lost")
	}
}

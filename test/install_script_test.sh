#!/usr/bin/env sh
# install_script_test.sh — unit tests for install.sh's OS/arch detection
# and download-URL construction, run in isolation from any real network
# access (there is no GitHub remote/release for this repo yet, so an
# actual end-to-end download cannot be tested — see the NOTE in install.sh).
#
# Run directly: sh test/install_script_test.sh
# Or via `go test`: see test/install_test.go, which shells out to this file.

set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)

# Source install.sh for its helper functions without running the installer.
CANOPY_INSTALL_SOURCE_ONLY=1
export CANOPY_INSTALL_SOURCE_ONLY
# shellcheck source=/dev/null
. "$repo_root/install.sh"

fail_count=0

assert_eq() {
	desc="$1"
	expected="$2"
	actual="$3"
	if [ "$expected" != "$actual" ]; then
		echo "FAIL: $desc"
		echo "  expected: $expected"
		echo "  actual:   $actual"
		fail_count=$((fail_count + 1))
	else
		echo "ok: $desc"
	fi
}

# --- OS detection, stubbed via a fake `uname` on PATH ---

with_fake_uname() {
	kernel_name="$1"
	machine="$2"
	shift 2
	tmp=$(mktemp -d)
	cat >"$tmp/uname" <<EOF
#!/usr/bin/env sh
case "\$1" in
-s) echo "$kernel_name" ;;
-m) echo "$machine" ;;
esac
EOF
	chmod +x "$tmp/uname"
	PATH="$tmp:$PATH" "$@"
	status=$?
	rm -rf "$tmp"
	return $status
}

os_linux=$(with_fake_uname Linux x86_64 canopy_detect_os)
assert_eq "detects Linux -> linux" "linux" "$os_linux"

os_darwin=$(with_fake_uname Darwin arm64 canopy_detect_os)
assert_eq "detects Darwin -> darwin" "darwin" "$os_darwin"

if with_fake_uname Windows_NT x86_64 canopy_detect_os >/tmp/canopy_test_out 2>&1; then
	echo "FAIL: expected unsupported OS to fail"
	fail_count=$((fail_count + 1))
else
	echo "ok: unsupported OS (Windows_NT) fails"
fi

arch_amd64=$(with_fake_uname Linux x86_64 canopy_detect_arch)
assert_eq "detects x86_64 -> amd64" "amd64" "$arch_amd64"

arch_amd64_alt=$(with_fake_uname Linux amd64 canopy_detect_arch)
assert_eq "detects amd64 -> amd64" "amd64" "$arch_amd64_alt"

arch_arm64=$(with_fake_uname Darwin arm64 canopy_detect_arch)
assert_eq "detects arm64 -> arm64" "arm64" "$arch_arm64"

arch_aarch64=$(with_fake_uname Linux aarch64 canopy_detect_arch)
assert_eq "detects aarch64 -> arm64" "arm64" "$arch_aarch64"

if with_fake_uname Linux i386 canopy_detect_arch >/tmp/canopy_test_out 2>&1; then
	echo "FAIL: expected unsupported arch to fail"
	fail_count=$((fail_count + 1))
else
	echo "ok: unsupported arch (i386) fails"
fi

# --- Asset name / download URL construction ---

assert_eq "asset name: linux/amd64/v1.2.3" \
	"canopy_v1.2.3_linux_amd64.tar.gz" \
	"$(canopy_asset_name linux amd64 v1.2.3)"

assert_eq "asset name: darwin/arm64/v1.2.3" \
	"canopy_v1.2.3_darwin_arm64.tar.gz" \
	"$(canopy_asset_name darwin arm64 v1.2.3)"

assert_eq "download URL: linux/amd64" \
	"https://github.com/asif/canopy/releases/download/v1.2.3/canopy_v1.2.3_linux_amd64.tar.gz" \
	"$(canopy_download_url asif/canopy v1.2.3 linux amd64)"

assert_eq "download URL: darwin/arm64" \
	"https://github.com/asif/canopy/releases/download/v1.2.3/canopy_v1.2.3_darwin_arm64.tar.gz" \
	"$(canopy_download_url asif/canopy v1.2.3 darwin arm64)"

assert_eq "download URL: darwin/amd64" \
	"https://github.com/asif/canopy/releases/download/v0.9.0/canopy_v0.9.0_darwin_amd64.tar.gz" \
	"$(canopy_download_url asif/canopy v0.9.0 darwin amd64)"

# --- Install dir resolution honors an explicit override ---

install_dir=$(CANOPY_INSTALL_DIR=/tmp/canopy-test-bin canopy_install_dir)
assert_eq "install dir honors CANOPY_INSTALL_DIR override" \
	"/tmp/canopy-test-bin" "$install_dir"

if [ "$fail_count" -ne 0 ]; then
	echo ""
	echo "$fail_count assertion(s) failed"
	exit 1
fi

echo ""
echo "all install.sh assertions passed"

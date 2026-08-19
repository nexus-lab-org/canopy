#!/usr/bin/env sh
# install.sh — curl-to-install script for canopy.
#
#   curl -fsSL https://raw.githubusercontent.com/nexus-lab-org/canopy/main/install.sh | sh
#
# Downloads the prebuilt canopy binary matching the running OS/arch from the
# latest GitHub Release of nexus-lab-org/canopy (built via .goreleaser.yaml at the
# repo root: linux/darwin, amd64/arm64, tar.gz archives) and installs it
# somewhere on PATH.
#
# This is the fallback install path for machines without a Go toolchain;
# `go install github.com/nexus-lab-org/canopy/cmd/canopy@latest` is the other
# supported path (see README.md).

set -eu

REPO="${CANOPY_INSTALL_REPO:-nexus-lab-org/canopy}"
VERSION="${CANOPY_INSTALL_VERSION:-latest}"
BIN_NAME="canopy"

# canopy_detect_os prints the goreleaser/GOOS-style OS name ("linux" or
# "darwin") for the current machine, derived from `uname -s`. Exits
# non-zero with a message on anything else (no Windows support, per the
# distribution decision in wayfinder/tickets/007-distribution-install-story.md).
canopy_detect_os() {
	uname_s=$(uname -s)
	case "$uname_s" in
	Linux) echo "linux" ;;
	Darwin) echo "darwin" ;;
	*)
		echo "canopy: unsupported OS '$uname_s' (only Linux and macOS are supported)" >&2
		return 1
		;;
	esac
}

# canopy_detect_arch prints the goreleaser/GOARCH-style arch name ("amd64"
# or "arm64") for the current machine, derived from `uname -m`.
canopy_detect_arch() {
	uname_m=$(uname -m)
	case "$uname_m" in
	x86_64 | amd64) echo "amd64" ;;
	arm64 | aarch64) echo "arm64" ;;
	*)
		echo "canopy: unsupported architecture '$uname_m' (only amd64 and arm64 are supported)" >&2
		return 1
		;;
	esac
}

# canopy_asset_name prints the archive filename goreleaser produces for a
# given (os, arch, version), matching the name_template in .goreleaser.yaml:
#   canopy_<version>_<os>_<arch>.tar.gz
# where <os>/<arch> are the raw GOOS/GOARCH values (lowercase, e.g.
# "linux"/"amd64"), per goreleaser's default `{{ .Os }}`/`{{ .Arch }}`
# template values (no case transform applied by the template itself).
canopy_asset_name() {
	os="$1"
	arch="$2"
	version="$3"
	echo "canopy_${version}_${os}_${arch}.tar.gz"
}

# canopy_download_url prints the full GitHub Releases download URL for the
# given repo, a *concrete* version tag (e.g. "v1.2.3" — not "latest"; see
# canopy_resolve_latest_tag for turning "latest" into a concrete tag first),
# os and arch.
canopy_download_url() {
	repo="$1"
	version="$2"
	os="$3"
	arch="$4"

	# The release tag keeps its "v" prefix (e.g. "v1.2.3"), but goreleaser's
	# {{ .Version }} archive name_template strips it (e.g. "canopy_1.2.3_..."),
	# so the asset filename needs the prefix stripped even though the URL
	# path segment for the tag itself does not.
	asset_version="${version#v}"
	asset="$(canopy_asset_name "$os" "$arch" "$asset_version")"
	echo "https://github.com/${repo}/releases/download/${version}/${asset}"
}

# canopy_resolve_latest_tag prints the tag name of the latest GitHub
# release for $REPO, using the GitHub API.
canopy_resolve_latest_tag() {
	repo="$1"
	curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" |
		grep '"tag_name"' |
		head -n1 |
		cut -d'"' -f4
}

# canopy_install_dir prints the directory to install the binary into: the
# first of $CANOPY_INSTALL_DIR, /usr/local/bin (if writable), or
# ~/.local/bin (created if necessary).
canopy_install_dir() {
	if [ -n "${CANOPY_INSTALL_DIR:-}" ]; then
		echo "$CANOPY_INSTALL_DIR"
		return 0
	fi
	if [ -w "/usr/local/bin" ]; then
		echo "/usr/local/bin"
		return 0
	fi
	echo "${HOME}/.local/bin"
}

canopy_install() {
	os="$(canopy_detect_os)"
	arch="$(canopy_detect_arch)"

	tag="$VERSION"
	if [ "$tag" = "latest" ]; then
		tag="$(canopy_resolve_latest_tag "$REPO")"
		if [ -z "$tag" ]; then
			echo "canopy: could not resolve the latest release tag for ${REPO}" >&2
			echo "canopy: is there a GitHub release published yet?" >&2
			return 1
		fi
	fi

	url="$(canopy_download_url "$REPO" "$tag" "$os" "$arch")"
	dest_dir="$(canopy_install_dir)"
	mkdir -p "$dest_dir"

	tmp_dir=$(mktemp -d)
	trap 'rm -rf "$tmp_dir"' EXIT

	echo "canopy: downloading ${url}"
	curl -fsSL "$url" -o "${tmp_dir}/canopy.tar.gz"

	tar -xzf "${tmp_dir}/canopy.tar.gz" -C "$tmp_dir" "$BIN_NAME"
	chmod +x "${tmp_dir}/${BIN_NAME}"
	mv "${tmp_dir}/${BIN_NAME}" "${dest_dir}/${BIN_NAME}"

	echo "canopy: installed to ${dest_dir}/${BIN_NAME}"
	case ":$PATH:" in
	*":${dest_dir}:"*) ;;
	*)
		echo "canopy: NOTE: ${dest_dir} is not on your PATH."
		echo "canopy:       add it, e.g.: export PATH=\"${dest_dir}:\$PATH\""
		;;
	esac
}

# Only run the installer when executed (or piped into a shell), not when
# sourced by a test harness that wants to call the helper functions above
# directly.
if [ "${CANOPY_INSTALL_SOURCE_ONLY:-}" != "1" ]; then
	canopy_install
fi

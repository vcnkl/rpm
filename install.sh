#!/bin/sh
set -e

REPO="vcnkl/rpm"
BINARY="rpm"
INSTALL_DIR="${HOME}/bin"

main() {
    os=$(detect_os)
    arch=$(detect_arch)

    if [ "$os" = "windows" ]; then
        echo "Error: Windows is not supported by this installer." >&2
        echo "Please download the binary manually from https://github.com/${REPO}/releases" >&2
        exit 1
    fi

    version=$(get_latest_version)
    if [ -z "$version" ]; then
        echo "Error: Could not determine latest version" >&2
        exit 1
    fi

    echo "Installing ${BINARY} ${version} for ${os}/${arch}..."

    archive_name="${BINARY}_${version#v}_${os}_${arch}.tar.gz"
    download_url="https://github.com/${REPO}/releases/download/${version}/${archive_name}"

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "$tmp_dir"' EXIT

    echo "Downloading ${download_url}..."
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$download_url" -o "${tmp_dir}/archive.tar.gz"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$download_url" -O "${tmp_dir}/archive.tar.gz"
    else
        echo "Error: curl or wget is required" >&2
        exit 1
    fi

    echo "Extracting..."
    tar -xzf "${tmp_dir}/archive.tar.gz" -C "$tmp_dir"

    mkdir -p "$INSTALL_DIR"
    mv "${tmp_dir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
    chmod +x "${INSTALL_DIR}/${BINARY}"

    echo ""
    echo "Successfully installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"

    if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        echo ""
        echo "Add ${INSTALL_DIR} to your PATH:"
        echo "  export PATH=\"\$HOME/bin:\$PATH\""
    fi
}

detect_os() {
    case "$(uname -s)" in
        Linux*)  echo "linux" ;;
        Darwin*) echo "darwin" ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *)
            echo "Error: Unsupported OS: $(uname -s)" >&2
            exit 1
            ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        arm64|aarch64) echo "arm64" ;;
        *)
            echo "Error: Unsupported architecture: $(uname -m)" >&2
            exit 1
            ;;
    esac
}

get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
    fi
}

main

#!/usr/bin/env bash
#
# install-k6.sh - Install k6 load testing tool
#
# This script installs k6 on macOS, Linux, or Windows (WSL/Git Bash)
# k6 is a modern load testing tool built for developers
#
# Usage:
#   ./scripts/install-k6.sh
#   make loadtest-install

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Darwin*)
            echo "macos"
            ;;
        Linux*)
            echo "linux"
            ;;
        MINGW*|MSYS*|CYGWIN*)
            echo "windows"
            ;;
        *)
            echo "unknown"
            ;;
    esac
}

# Check if k6 is already installed
check_existing_k6() {
    if command -v k6 &> /dev/null; then
        local version=$(k6 version --short 2>/dev/null || echo "unknown")
        log_success "k6 is already installed: ${version}"
        echo ""
        read -p "Reinstall? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log_info "Installation cancelled"
            exit 0
        fi
    fi
}

# Install k6 on macOS
install_macos() {
    log_info "Installing k6 on macOS..."

    if command -v brew &> /dev/null; then
        log_info "Using Homebrew to install k6..."
        brew install k6
        log_success "k6 installed successfully via Homebrew"
    else
        log_warning "Homebrew not found, using direct download..."
        install_direct "darwin"
    fi
}

# Install k6 on Linux
install_linux() {
    log_info "Installing k6 on Linux..."

    # Detect Linux distribution
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        case "$ID" in
            ubuntu|debian)
                install_debian
                ;;
            fedora|centos|rhel)
                install_rpm
                ;;
            *)
                log_warning "Distribution not detected, using direct download..."
                install_direct "linux"
                ;;
        esac
    else
        install_direct "linux"
    fi
}

# Install on Debian/Ubuntu
install_debian() {
    log_info "Installing k6 on Debian/Ubuntu..."

    # Add the k6 repository
    sudo gpg -k &> /dev/null || true
    sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
    echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list
    sudo apt-get update
    sudo apt-get install -y k6

    log_success "k6 installed successfully via apt"
}

# Install on Fedora/CentOS/RHEL
install_rpm() {
    log_info "Installing k6 on Fedora/CentOS/RHEL..."

    # Add the k6 repository
    cat <<EOF | sudo tee /etc/yum.repos.d/k6.repo
[k6]
name=k6
baseurl=https://dl.k6.io/rpm/stable/\$basearch
enabled=1
gpgcheck=1
gpgkey=https://dl.k6.io/key.gpg
EOF

    sudo yum install -y k6

    log_success "k6 installed successfully via yum"
}

# Direct download installation
install_direct() {
    local os=$1
    local arch=$(uname -m)
    local k6_arch=""

    # Map architecture
    case "$arch" in
        x86_64|amd64)
            k6_arch="amd64"
            ;;
        aarch64|arm64)
            k6_arch="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $arch"
            exit 1
            ;;
    esac

    log_info "Downloading k6 for ${os}/${k6_arch}..."

    # Get latest version
    local latest_version=$(curl -s https://api.github.com/repos/grafana/k6/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')

    if [ -z "$latest_version" ]; then
        log_error "Failed to get latest k6 version"
        exit 1
    fi

    log_info "Latest version: v${latest_version}"

    # Download URL
    local download_url="https://github.com/grafana/k6/releases/download/v${latest_version}/k6-v${latest_version}-${os}-${k6_arch}.tar.gz"

    # Download and extract
    local temp_dir=$(mktemp -d)
    cd "$temp_dir"

    log_info "Downloading from: ${download_url}"
    curl -L -o k6.tar.gz "$download_url"

    tar -xzf k6.tar.gz
    sudo mv "k6-v${latest_version}-${os}-${k6_arch}/k6" /usr/local/bin/k6
    sudo chmod +x /usr/local/bin/k6

    # Cleanup
    cd -
    rm -rf "$temp_dir"

    log_success "k6 installed successfully via direct download"
}

# Install on Windows (WSL/Git Bash)
install_windows() {
    log_info "Installing k6 on Windows..."
    log_warning "For Windows, we recommend using one of these methods:"
    echo ""
    echo "1. Install via Chocolatey:"
    echo "   choco install k6"
    echo ""
    echo "2. Install via Scoop:"
    echo "   scoop install k6"
    echo ""
    echo "3. Download from: https://dl.k6.io/msi/k6-latest-amd64.msi"
    echo ""
    log_error "Automated installation not supported on Windows"
    exit 1
}

# Verify installation
verify_installation() {
    log_info "Verifying k6 installation..."

    if ! command -v k6 &> /dev/null; then
        log_error "k6 command not found. Installation may have failed."
        exit 1
    fi

    local version=$(k6 version)
    log_success "k6 is installed and working!"
    echo ""
    echo "${version}"
    echo ""
}

# Print usage information
print_usage() {
    log_success "k6 is ready to use!"
    echo ""
    echo "📚 Quick Start:"
    echo ""
    echo "Run a load test:"
    echo "  k6 run loadtests/products-crud.js"
    echo ""
    echo "Run with custom options:"
    echo "  k6 run --vus 50 --duration 5m loadtests/products-crud.js"
    echo ""
    echo "Available tests:"
    echo "  • loadtests/products-crud.js       - Realistic CRUD mix"
    echo "  • loadtests/products-read-only.js  - Read-only baseline"
    echo "  • loadtests/ramp-up-test.js        - Gradual load increase"
    echo "  • loadtests/spike-test.js          - Traffic spike simulation"
    echo "  • loadtests/sustained-load.js      - Extended stability test"
    echo ""
    echo "Using Makefile:"
    echo "  make loadtest-crud      - Run CRUD mix test"
    echo "  make loadtest-ramp      - Run ramp-up test"
    echo "  make loadtest-spike     - Run spike test"
    echo "  make loadtest-sustained - Run sustained load test"
    echo ""
    echo "📖 Documentation:"
    echo "  • k6 docs:       https://k6.io/docs/"
    echo "  • Load testing:  wiki/LOAD_TESTING.md"
    echo ""
}

# Main installation flow
main() {
    echo ""
    log_info "k6 Load Testing Tool Installer"
    echo ""

    # Check if already installed
    check_existing_k6

    # Detect OS and install
    local os=$(detect_os)

    case "$os" in
        macos)
            install_macos
            ;;
        linux)
            install_linux
            ;;
        windows)
            install_windows
            ;;
        *)
            log_error "Unsupported operating system"
            exit 1
            ;;
    esac

    # Verify installation
    verify_installation

    # Print usage
    print_usage
}

# Run main function
main "$@"

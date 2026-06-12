#!/usr/bin/env bash
#
# Check or install llama.cpp tools used by the real local-LLM benchmark path.

set -euo pipefail

DEFAULT_HF_MODEL="${LAMINAR_LLAMA_HF_MODEL:-bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M}"
INSTALL=false

usage() {
  cat <<USAGE
Usage: $0 [--install]

Checks for llama-server and llama-cli. With --install, installs llama.cpp via
Homebrew when the tools are missing.

Environment:
  LAMINAR_LLAMA_HF_MODEL   Hugging Face GGUF model spec used by the run script
                           Default: $DEFAULT_HF_MODEL
USAGE
}

while (($# > 0)); do
  case "$1" in
    --install)
      INSTALL=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

missing=()
if ! command -v llama-server >/dev/null 2>&1; then
  missing+=("llama-server")
fi
if ! command -v llama-cli >/dev/null 2>&1; then
  missing+=("llama-cli")
fi

if ((${#missing[@]} == 0)); then
  echo "llama.cpp tools are available:"
  echo "  llama-server: $(command -v llama-server)"
  echo "  llama-cli:    $(command -v llama-cli)"
  echo "default model: $DEFAULT_HF_MODEL"
  exit 0
fi

echo "missing llama.cpp tools: ${missing[*]}"

if [[ "$INSTALL" != true ]]; then
  cat <<EOF

Install options:
  $0 --install
  brew install llama.cpp

After install, start a warmed server with:
  ./scripts/run_llama_server.sh --background
EOF
  exit 1
fi

if ! command -v brew >/dev/null 2>&1; then
  echo "Homebrew is required for --install on this setup." >&2
  echo "Install llama.cpp manually from https://github.com/ggml-org/llama.cpp." >&2
  exit 1
fi

echo "installing llama.cpp with Homebrew..."
brew install llama.cpp

echo "verifying install..."
command -v llama-server
command -v llama-cli
echo "llama.cpp setup complete"

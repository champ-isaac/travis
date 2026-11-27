#!/usr/bin/env bash
# Standalone MLX environment for Inkling 2 Mini (inkling2_loop) on Apple Silicon.
#
# Usage:
#   chmod +x install_mlx_inkling2.sh
#   ./install_mlx_inkling2.sh              # install env + download model (~17 GB)
#   SKIP_MODEL=1 ./install_mlx_inkling2.sh # install env only
#   SERVE=1 ./install_mlx_inkling2.sh      # then start mlx_lm.server on 127.0.0.1:${PORT:-8080} (foreground, Ctrl-C stops)
#   MODEL_REPO=mlx-community/<other-model> MLX_LM_PKG=mlx-lm ./install_mlx_inkling2.sh
#
# This uses uv to create the venv with Python 3.12 (an installed one, or one uv
# downloads). A newer system Python is left untouched.

set -euo pipefail

BASE_DIR="${BASE_DIR:-$HOME/mlx}"
VENV_DIR="$BASE_DIR/.venv"
PY_VERSION="${PY_VERSION:-3.12}"
MODEL_REPO="${MODEL_REPO:-mlx-community/Inkling-2-Mini-4bit}"
MODEL_DIR="$BASE_DIR/models/$(basename "$MODEL_REPO")"
# Inkling 2 Mini isn't in a mlx-lm release yet; the model card says to use PR #2086.
# Pinned to a PR #2086 commit so re-runs don't change. Swap this for plain "mlx-lm"
# when running other models or once the PR is merged.
MLX_LM_PKG="${MLX_LM_PKG:-git+https://github.com/ml-explore/mlx-lm.git@e2b5bbd3bd9d186dea895e60802aa765e70b9d93}"

log() { printf '\n==> %s\n' "$*"; }

# --- 0. Sanity checks -------------------------------------------------------
if [[ "$(uname -s)" != "Darwin" || "$(sysctl -n hw.optional.arm64 2>/dev/null)" != 1 ]]; then
  echo "This script needs macOS on Apple Silicon (arm64)." >&2
  exit 1
fi
if (( $(sw_vers -productVersion | cut -d. -f1) < 14 )); then
  echo "mlx needs macOS 14 or newer (this Mac has $(sw_vers -productVersion))." >&2
  exit 1
fi
xcode-select -p >/dev/null 2>&1 || { echo "Command Line Tools (git) not found. Run: xcode-select --install" >&2; exit 1; }

# --- 1. uv ------------------------------------------------------------------
if ! command -v uv >/dev/null; then
  log "Installing uv"
  if command -v brew >/dev/null; then
    brew install uv
  else
    # Runs Astral's install script, fetched live and unpinned. To avoid that,
    # install uv yourself first (e.g. via Homebrew) and re-run this script.
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
  fi
fi
log "uv $(uv --version)"

# --- 2. Virtual environment (Python $PY_VERSION) ----------------------------
mkdir -p "$BASE_DIR/models"
if [[ ! -x "$VENV_DIR/bin/python" ]]; then
  log "Creating venv at $VENV_DIR with Python $PY_VERSION"
  uv venv --python "cpython-$PY_VERSION-macos-aarch64" "$VENV_DIR"
else
  log "Reusing existing venv at $VENV_DIR"
  [[ "$("$VENV_DIR/bin/python" -c 'import platform; print(platform.python_version())')." == "$PY_VERSION."* ]] \
    || echo "Warning: the existing venv's Python is not $PY_VERSION. Delete $VENV_DIR to recreate it." >&2
fi
PY="$VENV_DIR/bin/python"
"$PY" --version

# --- 3. Packages ------------------------------------------------------------
log "Installing MLX + mlx-lm + deps"
uv pip install --python "$PY" -U mlx "$MLX_LM_PKG" huggingface_hub

# --- 4. Verify this mlx-lm build supports the model (config.json only) ------
# This only proves mlx-lm has a module for the model's model_type. It does not
# prove the weights load (a name mismatch like that broke the mlx-vlm attempt);
# the smoke test in step 5 does.
log "Checking mlx-lm supports $MODEL_REPO"
CFG_DIR="$(mktemp -d)"
trap 'rm -rf "$CFG_DIR"' EXIT
"$VENV_DIR/bin/hf" download "$MODEL_REPO" config.json --local-dir "$CFG_DIR" >/dev/null
if ! "$PY" - "$CFG_DIR/config.json" <<'PYEOF'
import importlib, json, sys
mt = json.load(open(sys.argv[1]))["model_type"]
target = mt
try:
    from mlx_lm.utils import MODEL_REMAPPING
    target = MODEL_REMAPPING.get(mt, mt)
    importlib.import_module(f"mlx_lm.models.{target}")
except Exception as e:
    print(f"Cannot load mlx_lm.models.{target} for {mt}: {e!r}", file=sys.stderr)
    sys.exit(1)
print(f"{mt} -> mlx_lm.models.{target}: OK")
PYEOF
then
  echo "This mlx-lm build can't load the model. Stopping before the model download." >&2
  exit 1
fi
"$PY" -c "import mlx.core as mx, mlx_lm; print('mlx', mx.__version__, '| mlx_lm', mlx_lm.__version__, '| device', mx.default_device())"

# --- 5. Model download ------------------------------------------------------
if [[ "${SKIP_MODEL:-0}" != "1" ]]; then
  free_gb=$(( $(df -Pk "$BASE_DIR" | awk 'NR==2 {print $4}') / 1048576 ))
  if (( free_gb < ${MIN_FREE_GB:-20} )); then
    echo "Only $free_gb GB free in $BASE_DIR; the model needs about ${MIN_FREE_GB:-20} GB. Free space, or set MIN_FREE_GB to override." >&2
    exit 1
  fi
  log "Downloading $MODEL_REPO -> $MODEL_DIR"
  "$VENV_DIR/bin/hf" download "$MODEL_REPO" --local-dir "$MODEL_DIR"

  ram_gb=$(( $(sysctl -n hw.memsize) / 1073741824 ))
  (( ram_gb >= 24 )) || echo "Warning: $ram_gb GB RAM; the smoke test peaked at 17.4 GB for Inkling 2 Mini and may swap or fail." >&2
  log "Smoke test"
  out="$("$PY" -m mlx_lm generate --model "$MODEL_DIR" \
    --prompt "Say hello in one sentence." --max-tokens 400)"
  printf '%s\n' "$out"
  n="$(sed -n 's/^Generation: \([0-9]*\) tokens.*/\1/p' <<<"$out")"
  (( ${n:-0} > 0 && ${n:-0} < 400 )) || { echo "Smoke test failed: ${n:-0} tokens generated; the reply was empty or hit the 400-token limit." >&2; exit 1; }
fi

cat <<EOF

Done.

Activate:     source "$VENV_DIR/bin/activate"
EOF
if [[ "${SKIP_MODEL:-0}" == "1" ]]; then
  echo "Model not downloaded (SKIP_MODEL=1). Re-run without it to download $MODEL_REPO."
else
  cat <<EOF
Chat test:    python -m mlx_lm generate --model "$MODEL_DIR" --prompt "Hi" --max-tokens 200
Serve (8080): python -m mlx_lm server --model "$MODEL_DIR" --port 8080
EOF
fi

# --- 6. Optional: start the server (opt-in, so a plain run still only installs) ---
if [[ "${SERVE:-0}" == "1" ]]; then
  [[ -f "$MODEL_DIR/config.json" ]] || { echo "No model at $MODEL_DIR; run without SKIP_MODEL first." >&2; exit 1; }
  if lsof -nP -iTCP:"${PORT:-8080}" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Port ${PORT:-8080} is already in use; not starting a second server." >&2
    exit 1
  fi
  log "Serving $MODEL_DIR on 127.0.0.1:${PORT:-8080}"
  exec "$PY" -m mlx_lm server --model "$MODEL_DIR" --host 127.0.0.1 --port "${PORT:-8080}"
fi

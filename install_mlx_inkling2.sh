#!/usr/bin/env bash
# Standalone MLX environment for Inkling 2 Mini (inkling2_loop) on Apple Silicon.
#
# Usage:
#   chmod +x install_mlx_inkling2.sh
#   ./install_mlx_inkling2.sh              # install env + download model (~17 GB)
#   SKIP_MODEL=1 ./install_mlx_inkling2.sh # install env only
#   MODEL_REPO=mlx-community/<other-model> MLX_LM_PKG=mlx-lm ./install_mlx_inkling2.sh
#
# Your system Python (3.15) is too new for some wheels (torch in particular),
# so this uses uv to create the venv with its own managed Python 3.12.
# Your system Python is left untouched.

set -euo pipefail

BASE_DIR="${BASE_DIR:-$HOME/mlx}"
VENV_DIR="$BASE_DIR/.venv"
PY_VERSION="${PY_VERSION:-3.12}"
MODEL_REPO="${MODEL_REPO:-mlx-community/Inkling-2-Mini-4bit}"
MODEL_DIR="$BASE_DIR/models/$(basename "$MODEL_REPO")"
# Inkling 2 Mini isn't in a mlx-lm release yet; the model card says to use PR #2086.
# Swap this for plain "mlx-lm" when running other models or once the PR is merged.
MLX_LM_PKG="${MLX_LM_PKG:-git+https://github.com/ml-explore/mlx-lm.git@refs/pull/2086/head}"

log() { printf '\n==> %s\n' "$*"; }

# --- 0. Sanity checks -------------------------------------------------------
if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
  echo "This script needs macOS on Apple Silicon (arm64)." >&2
  exit 1
fi
command -v git >/dev/null || { echo "git not found. Run: xcode-select --install" >&2; exit 1; }

# --- 1. uv ------------------------------------------------------------------
if ! command -v uv >/dev/null; then
  log "Installing uv"
  if command -v brew >/dev/null; then
    brew install uv
  else
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
  fi
fi
log "uv $(uv --version)"

# --- 2. Virtual environment (Python $PY_VERSION) ----------------------------
mkdir -p "$BASE_DIR/models"
if [[ ! -x "$VENV_DIR/bin/python" ]]; then
  log "Creating venv at $VENV_DIR with Python $PY_VERSION"
  uv venv --python "$PY_VERSION" "$VENV_DIR"
else
  log "Reusing existing venv at $VENV_DIR"
fi
PY="$VENV_DIR/bin/python"
"$PY" --version

# --- 3. Packages ------------------------------------------------------------
log "Installing MLX + mlx-lm + deps"
uv pip install --python "$PY" -U mlx "$MLX_LM_PKG" huggingface_hub

# --- 4. Verify this mlx-lm build supports the model (config.json only) ------
log "Checking mlx-lm supports $MODEL_REPO"
CFG_DIR="$(mktemp -d)"
"$VENV_DIR/bin/hf" download "$MODEL_REPO" config.json --local-dir "$CFG_DIR" >/dev/null
if ! "$PY" - "$CFG_DIR/config.json" <<'PYEOF'
import importlib, json, sys
from mlx_lm.utils import MODEL_REMAPPING
mt = json.load(open(sys.argv[1]))["model_type"]
target = MODEL_REMAPPING.get(mt, mt)
try:
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
rm -rf "$CFG_DIR"
"$PY" -c "import mlx.core as mx, mlx_lm; print('mlx', mx.__version__, '| mlx_lm', mlx_lm.__version__, '| device', mx.default_device())"

# --- 5. Model download ------------------------------------------------------
if [[ "${SKIP_MODEL:-0}" != "1" ]]; then
  log "Downloading $MODEL_REPO -> $MODEL_DIR"
  "$VENV_DIR/bin/hf" download "$MODEL_REPO" --local-dir "$MODEL_DIR"

  log "Smoke test"
  "$PY" -m mlx_lm generate --model "$MODEL_DIR" \
    --prompt "Say hello in one sentence." --max-tokens 100
fi

cat <<EOF

Done.

Activate:     source "$VENV_DIR/bin/activate"
Chat test:    python -m mlx_lm generate --model "$MODEL_DIR" --prompt "Hi" --max-tokens 200
Serve (8080): python -m mlx_lm server --model "$MODEL_DIR" --port 8080
EOF
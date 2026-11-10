#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
SERVER="${ZETA_SERVER:-http://zeta.local}"

if ! command -v go >/dev/null 2>&1; then
  echo "erreur: Go est requis pour compiler zeta" >&2
  exit 1
fi
if ! command -v make >/dev/null 2>&1; then
  echo "erreur: make est requis" >&2
  exit 1
fi

make -C "$ROOT/cli" build
install -d "$BIN_DIR"
install -m 0755 "$ROOT/cli/bin/zeta" "$BIN_DIR/zeta"

echo "zeta installé dans $BIN_DIR/zeta"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    cat <<MSG

Ajoutez $BIN_DIR au PATH :

  export PATH="$BIN_DIR:\$PATH"

Pour le rendre permanent, ajoutez cette ligne à ~/.bashrc ou ~/.zshrc.
MSG
    ;;
esac

cat <<MSG

Connexion :

  zeta login --server $SERVER --email dev@example.com --password dev

Puis, depuis un projet :

  zeta build
  zeta build --sync   # sync bidirectionnelle pendant le build
MSG

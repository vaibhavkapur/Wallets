#!/usr/bin/env bash
# Runs the MyWalletKit test suite on macOS.
#
# With Xcode installed, plain `swift test` works. With only the Command Line
# Tools, Swift Testing's framework is not on the default search path, so the
# paths are passed explicitly.
set -euo pipefail
cd "$(dirname "$0")/../ios/MyWalletKit"

if xcode-select -p 2>/dev/null | grep -q "Xcode.app"; then
  exec swift test "$@"
fi

CLT="$(xcode-select -p)"
FW="$CLT/Library/Developer/Frameworks"
LIB="$CLT/Library/Developer/usr/lib"
if [ ! -d "$FW/Testing.framework" ]; then
  echo "Testing.framework not found under $FW; install Xcode or newer Command Line Tools" >&2
  exit 1
fi
exec swift test \
  -Xswiftc -F -Xswiftc "$FW" \
  -Xlinker -F -Xlinker "$FW" \
  -Xlinker -rpath -Xlinker "$FW" \
  -Xlinker -rpath -Xlinker "$LIB" \
  "$@"

#!/usr/bin/env bash
# Tests for the pack/verify tail of build-sysext.sh. These drive the `nvidia`
# code path, which packs a caller-supplied tree and never touches apt or the
# network, so the whole script runs offline. `sudo` and `mksquashfs` are stubbed
# on PATH: `sudo` just execs its arguments and `mksquashfs` writes an output
# file whose size the test controls, letting us prove the two regressions are
# fixed:
#   1. The tree is packed (payload present, non-empty image) -> build succeeds.
#   2. An empty tree -> the build fails loudly instead of shipping a hollow raw.
#   3. A too-small squashfs -> the build fails loudly instead of ignoring it.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
script=${here}/build-sysext.sh
pass=0
fail=0

setup() {
	root=$(mktemp -d)
	stub=$(mktemp -d)
	mkdir -p "${root}/rootfs/etc" "${root}/rootfs/lib/modules/6.1.0-feldcore"
	: >"${root}/rootfs/etc/os-release"
	export FELDCORE_OS_WORK="${root}/work"
	export FELDCORE_NVIDIA_SYSEXT=1
	export FELDCORE_NVIDIA_DIR="${root}/nv"
	mkdir -p "${FELDCORE_NVIDIA_DIR}"

	# sudo stub: run the command without privilege (we already own everything).
	cat >"${stub}/sudo" <<'EOF'
#!/usr/bin/env bash
exec "$@"
EOF
	# mksquashfs stub: write DEST ($2) with STUB_SQUASHFS_BYTES bytes of zeros.
	cat >"${stub}/mksquashfs" <<'EOF'
#!/usr/bin/env bash
dest=$2
head -c "${STUB_SQUASHFS_BYTES:-200000}" /dev/zero >"${dest}"
EOF
	chmod +x "${stub}/sudo" "${stub}/mksquashfs"
	export PATH="${stub}:${PATH}"
}

teardown() {
	rm -rf "${root}" "${stub}"
	unset FELDCORE_OS_WORK FELDCORE_NVIDIA_SYSEXT FELDCORE_NVIDIA_DIR STUB_SQUASHFS_BYTES
}

check() {
	local name=$1 want=$2 got=$3
	if [[ ${want} == "${got}" ]]; then
		echo "ok   - ${name}"
		pass=$((pass + 1))
	else
		echo "FAIL - ${name}: wanted ${want}, got ${got}" >&2
		fail=$((fail + 1))
	fi
}

# 1. Real payload + plausible image size -> success.
setup
echo "payload" >"${FELDCORE_NVIDIA_DIR}/nvidia-ctk"
export STUB_SQUASHFS_BYTES=200000
out=${root}/nvidia-container-toolkit.raw
rc=0
bash "${script}" nvidia "${root}/rootfs" "${out}" >/dev/null 2>&1 || rc=$?
check "good payload packs successfully" 0 "${rc}"
check "output image was written" 1 "$([[ -f ${out} ]] && echo 1 || echo 0)"
teardown

# 2. Empty payload (no package content landed) -> loud failure, no image.
setup
export STUB_SQUASHFS_BYTES=200000
out=${root}/nvidia-container-toolkit.raw
rc=0
err=$(bash "${script}" nvidia "${root}/rootfs" "${out}" 2>&1 >/dev/null) || rc=$?
check "empty payload fails the build" 1 "$([[ ${rc} -ne 0 ]] && echo 1 || echo 0)"
check "empty payload names the problem" 1 "$(grep -qi 'no payload' <<<"${err}" && echo 1 || echo 0)"
check "empty payload wrote no image" 0 "$([[ -f ${out} ]] && echo 1 || echo 0)"
teardown

# 3. Too-small squashfs (e.g. the 4096-byte empty image) -> loud failure.
setup
echo "payload" >"${FELDCORE_NVIDIA_DIR}/nvidia-ctk"
export STUB_SQUASHFS_BYTES=4096
out=${root}/nvidia-container-toolkit.raw
rc=0
err=$(bash "${script}" nvidia "${root}/rootfs" "${out}" 2>&1 >/dev/null) || rc=$?
check "tiny squashfs fails the build" 1 "$([[ ${rc} -ne 0 ]] && echo 1 || echo 0)"
check "tiny squashfs names the problem" 1 "$(grep -qi 'too small' <<<"${err}" && echo 1 || echo 0)"
teardown

echo
echo "passed ${pass}, failed ${fail}"
((fail == 0))

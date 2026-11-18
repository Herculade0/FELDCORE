#!/usr/bin/env bash
# Fail if any sysext the signed manifest lists is missing from the build
# output, has a size the manifest does not agree with, or is too small to be a
# real extension image. Run this before the release is published so a signed
# manifest can never point at an asset that was never uploaded. That gap is
# what made VM Manager's "Install libvirt" fail with "release server returned
# 404": the manifest listed libvirt.raw but the release never carried it.
set -euo pipefail

dir=${1:?usage: check-sysexts.sh DIST_DIR}
manifest=${dir}/feldcore-os.json
[[ -f ${manifest} ]] || { echo "no manifest at ${manifest}" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

# An empty squashfs is about 4 KiB, so a real extension is far larger. This
# floor catches a sysext whose package payload never landed in the tree, which
# is how nvidia-container-toolkit.raw shipped at 4096 bytes.
min=65536

names=$(jq -r '.sysexts[]?.name' "${manifest}")
if [[ -z ${names} ]]; then
	echo "The manifest lists no sysexts; nothing to check."
	exit 0
fi

fail=0
while IFS= read -r name; do
	name=${name%$'\r'}
	[[ -n ${name} ]] || continue
	path=${dir}/sysexts/${name}
	if [[ ! -f ${path} ]]; then
		echo "The manifest lists ${name} but ${path} was not built." >&2
		fail=1
		continue
	fi
	size=$(stat -c %s "${path}")
	want=$(jq -r --arg n "${name}" '.sysexts[] | select(.name==$n) | .size' "${manifest}")
	if [[ ${size} != "${want}" ]]; then
		echo "${name}: on-disk size ${size} does not match the manifest's ${want}." >&2
		fail=1
	fi
	if ((size < min)); then
		echo "${name}: ${size} bytes is too small to be a real extension (empty squashfs?)." >&2
		fail=1
	fi
done <<<"${names}"

((fail == 0)) || exit 1
echo "All manifest sysext assets are present and plausibly sized."

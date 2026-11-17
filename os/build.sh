#!/bin/bash
# Build the FELDCORE OS disk image, the installer image, and a signed update.
# Private signing keys stay in $FELDCORE_OS_WORK (default /var/tmp/feldcore-os).
# They are never written into the image or the output directory.
set -euo pipefail
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.11}"

repo=$(cd "$(dirname "$0")/.." && pwd)
work=${FELDCORE_OS_WORK:-/var/tmp/feldcore-os}
out=${FELDCORE_OS_OUT:-${repo}/dist/os}
version=${FELDCORE_OS_VERSION:-0.1.0}
builder=${FELDCORE_IMAGE_BUILDER:-debootstrap}
root_mib=${FELDCORE_ROOT_MIB:-6144}
esp_mib=256
state_mib=${FELDCORE_STATE_MIB:-8192}
slot_slack=128
mirror=${FELDCORE_DEBIAN_MIRROR:-http://deb.debian.org/debian}

rootfs=${work}/rootfs
mnt=${work}/mnt
mounts=()
loops=()

log() { printf 'feldcore-os: %s\n' "$*"; }
die() { printf 'feldcore-os: %s\n' "$*" >&2; exit 1; }

cleanup() {
	local m l
	for m in "${mounts[@]:-}"; do
		sudo umount "$m" 2>/dev/null || true
	done
	for l in "${loops[@]:-}"; do
		sudo losetup -d "$l" 2>/dev/null || true
	done
}
trap cleanup EXIT

track_mount() { mounts+=("$1"); }
track_loop() { loops+=("$1"); }

unmount_rootfs() {
	local p
	for p in dev/pts dev proc sys run; do
		if mountpoint -q "${rootfs}/${p}" 2>/dev/null; then
			sudo umount "${rootfs}/${p}" || true
		fi
	done
}

require() {
	local c
	for c in "$@"; do
		command -v "$c" >/dev/null 2>&1 || die "missing command: $c"
	done
}

debootstrap_root() {
	if [[ -f ${rootfs}/etc/debian_version && ! -d ${rootfs}/debootstrap ]]; then
		log "reusing ${rootfs}"
		return
	fi
	log "debootstrap debian trixie"
	sudo mkdir -p "${rootfs}"
	sudo debootstrap --variant=minbase trixie "${rootfs}" "${mirror}"
}

mkosi_root() {
	log "mkosi directory build"
	sudo mkdir -p "${work}/mkosi-out"
	sudo mkosi -C "${repo}/os/mkosi" --output-dir "${work}/mkosi-out" --force build
	sudo rm -rf "${rootfs}"
	sudo mv "${work}/mkosi-out/rootfs" "${rootfs}"
}

install_packages() {
	if [[ -f ${rootfs}/etc/feldcore-packages && ${FELDCORE_OS_REPACKAGES:-0} != 1 ]]; then
		log "packages already installed"
		return
	fi
	log "installing packages"
	sudo cp /etc/resolv.conf "${rootfs}/etc/resolv.conf"
	printf 'deb %s trixie main non-free-firmware\ndeb http://deb.debian.org/debian-security trixie-security main non-free-firmware\ndeb %s trixie-updates main non-free-firmware\n' \
		"${mirror}" "${mirror}" | sudo tee "${rootfs}/etc/apt/sources.list" >/dev/null
	printf '#!/bin/sh\nexit 101\n' | sudo tee "${rootfs}/usr/sbin/policy-rc.d" >/dev/null
	sudo chmod 755 "${rootfs}/usr/sbin/policy-rc.d"
	printf 'APT::Install-Recommends "false";\nAPT::Install-Suggests "false";\n' |
		sudo tee "${rootfs}/etc/apt/apt.conf.d/00no-recommends" >/dev/null
	sudo mkdir -p "${rootfs}/proc" "${rootfs}/sys" "${rootfs}/dev" "${rootfs}/dev/pts" "${rootfs}/run"
	sudo mount --bind /proc "${rootfs}/proc"
	sudo mount --bind /sys "${rootfs}/sys"
	sudo mount --bind /dev "${rootfs}/dev"
	sudo mount --bind /dev/pts "${rootfs}/dev/pts"
	track_mount "${rootfs}/dev/pts"
	track_mount "${rootfs}/dev"
	track_mount "${rootfs}/sys"
	track_mount "${rootfs}/proc"
	local pkgs
	pkgs=$(grep -v '^[[:space:]]*#' "${repo}/os/packages.txt" | grep -v '^[[:space:]]*$' | tr '\n' ' ')
	sudo chroot "${rootfs}" apt-get update
	sudo chroot "${rootfs}" env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ${pkgs}
	configure_containerd
	sudo chroot "${rootfs}" apt-get clean
	unmount_rootfs
	mounts=()
	sudo touch "${rootfs}/etc/feldcore-packages"
}

build_binary() {
	if [[ ${FELDCORE_OS_REBUILD_DAEMON:-1} == 0 && -x ${work}/feldcored ]]; then
		return
	fi
	log "building feldcored"
	local ldflags vite_version
	ldflags="${FELDCORE_LDFLAGS:--s -w -X github.com/Herculade0/feldcore/internal/version.Current=${version}}"
	vite_version="${VITE_FELDCORE_VERSION:-${version}}"
	(cd "${repo}/web" && npm install --no-audit --no-fund && VITE_FELDCORE_VERSION="${vite_version}" npm run build)
	(cd "${repo}" && CGO_ENABLED=0 go build -trimpath -ldflags "${ldflags}" -o "${work}/feldcored" ./cmd/feldcored)
}

signing_key() {
	mkdir -p "${work}/keys"
	chmod 700 "${work}/keys"
	if [[ -n ${FELDCORE_UPDATE_KEY:-} ]]; then
		[[ -f ${FELDCORE_UPDATE_KEY} ]] || die "FELDCORE_UPDATE_KEY does not exist"
		(cd "${repo}" && go run ./cmd/osmanifest pub -key "${FELDCORE_UPDATE_KEY}" -pub "${work}/keys/update.pub")
		return
	fi
	if [[ ! -f ${work}/keys/update.key ]]; then
		(cd "${repo}" && go run ./cmd/osmanifest genkey -pub "${work}/keys/update.pub" -key "${work}/keys/update.key")
	fi
	FELDCORE_UPDATE_KEY=${work}/keys/update.key
}

apply_image() {
	unmount_rootfs
	if mountpoint -q "${rootfs}/proc" || mountpoint -q "${rootfs}/dev"; then
		die "the rootfs still has a host mount"
	fi
	log "applying the FELDCORE image"
	sudo mkdir -p "${rootfs}/usr/bin" "${rootfs}/usr/share/feldcore" "${rootfs}/efi" "${rootfs}/var/lib/feldcore"
	sudo cp -a "${repo}/os/image/." "${rootfs}/"
	sudo cp "${work}/feldcored" "${rootfs}/usr/bin/feldcored"
	sudo chmod 755 "${rootfs}/usr/bin/feldcored"
	sudo cp "${work}/keys/update.pub" "${rootfs}/usr/share/feldcore/update.pub"
	sudo chmod 755 \
		"${rootfs}/usr/lib/feldcore/no-apt" \
		"${rootfs}/usr/lib/feldcore/prepare-state" \
		"${rootfs}/usr/lib/feldcore/apply-mounts" \
		"${rootfs}/usr/lib/feldcore/smoke-boot" \
		"${rootfs}/usr/lib/feldcore/bless-boot" \
		"${rootfs}/usr/sbin/feldcore-install"
	sudo rm -f "${rootfs}/usr/sbin/policy-rc.d"
	install_cloudflared
	install_caddy
	sudo chroot "${rootfs}" groupadd -f kvm
	sudo chroot "${rootfs}" groupadd -f libvirt
	sudo chroot "${rootfs}" groupadd -f docker
	if ! sudo chroot "${rootfs}" getent passwd libvirt-qemu >/dev/null; then
		sudo chroot "${rootfs}" useradd -r -g libvirt -G kvm -d /var/lib/libvirt -s /usr/sbin/nologin libvirt-qemu
	fi
	sudo rm -rf "${rootfs}/var/lib/libvirt"
	sudo ln -sfn /var/lib/feldcore/libvirt "${rootfs}/var/lib/libvirt"
	local kver
	kver=$(sudo ls "${rootfs}/lib/modules" | head -1 || true)
	sudo tee "${rootfs}/etc/os-release" >/dev/null <<EOF
NAME="FELDCORE OS"
ID=feldcore
ID_LIKE=debian
PRETTY_NAME="FELDCORE OS ${version}"
VERSION_ID=${version}
VERSION=${version}
SYSEXT_LEVEL=1
HOME_URL="https://github.com/Herculade0/FELDCORE"
EOF
	sudo tee "${rootfs}/etc/feldcore-os-release" >/dev/null <<EOF
ID=feldcore
VERSION_ID=${version}
VERSION=${version}
SYSEXT_LEVEL=1
KERNEL=${kver}
EOF
	sudo ln -sfn /var/lib/feldcore/sysexts "${rootfs}/var/lib/extensions"
	sudo mkdir -p \
		"${rootfs}/etc/systemd/system/multi-user.target.wants" \
		"${rootfs}/etc/systemd/system/getty.target.wants" \
		"${rootfs}/etc/systemd/system/network-online.target.wants"
	local unit
	for unit in \
		feldcore.service feldcore-state.service feldcore-bless.service feldcore-install.service \
		feldcore-mounts.service feldcore-smoke.service \
		systemd-networkd.service systemd-timesyncd.service \
		docker.service docker.socket containerd.service; do
		sudo ln -sfn "/usr/lib/systemd/system/${unit}" \
			"${rootfs}/etc/systemd/system/multi-user.target.wants/${unit}"
	done
	sudo ln -sfn /usr/lib/systemd/system/systemd-networkd.service \
		"${rootfs}/etc/systemd/system/network-online.target.wants/systemd-networkd.service"
	sudo ln -sfn /usr/lib/systemd/system/serial-getty@.service \
		"${rootfs}/etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service"
	# The root account cannot log in. The installer image adds a serial autologin of its own.
	sudo chroot "${rootfs}" passwd -l root
	local apt
	for apt in apt apt-get apt-cache apt-mark apt-cdrom; do
		if [[ -e ${rootfs}/usr/bin/${apt} || -L ${rootfs}/usr/bin/${apt} ]]; then
			sudo cp "${rootfs}/usr/lib/feldcore/no-apt" "${rootfs}/usr/bin/${apt}"
			sudo chmod 755 "${rootfs}/usr/bin/${apt}"
		fi
	done
	sudo tee "${rootfs}/etc/apt/sources.list" >/dev/null </dev/null
	sudo rm -rf "${rootfs}/var/lib/apt/lists/"* "${rootfs}/var/cache/apt/archives/"*.deb || true
	sudo tee "${rootfs}/etc/resolv.conf" >/dev/null <<EOF
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF
	sudo ln -sfn /usr/share/zoneinfo/UTC "${rootfs}/etc/localtime"
	sudo rm -rf "${rootfs}/proc" "${rootfs}/sys" "${rootfs}/dev" "${rootfs}/run" "${rootfs}/tmp"
	sudo mkdir -p "${rootfs}/proc" "${rootfs}/sys" "${rootfs}/dev" "${rootfs}/run" "${rootfs}/tmp" "${rootfs}/root" "${rootfs}/home"
	sudo chmod 1777 "${rootfs}/tmp"
}

kernel_files() {
	local link=${rootfs}/boot/vmlinuz
	if [[ ! -e ${link} ]]; then
		link=$(sudo find "${rootfs}/boot" -maxdepth 1 -name 'vmlinuz-*' -print -quit)
	fi
	[[ -n ${link} && -e ${link} ]] || die "the rootfs has no kernel"
	sudo cp -L "${link}" "${work}/vmlinuz"
	local initrd=${rootfs}/boot/initrd.img
	if [[ ! -e ${initrd} ]]; then
		initrd=$(sudo find "${rootfs}/boot" -maxdepth 1 -name 'initrd.img*' -print -quit)
	fi
	[[ -n ${initrd} && -e ${initrd} ]] || die "the rootfs has no initrd"
	sudo cp -L "${initrd}" "${work}/initrd.img"
	local efi=${rootfs}/usr/lib/systemd/boot/efi/systemd-bootx64.efi
	[[ -f ${efi} ]] || die "the rootfs has no systemd-boot EFI binary"
	sudo cp "${efi}" "${work}/systemd-bootx64.efi"
	sudo chown "$(id -u):$(id -g)" "${work}/vmlinuz" "${work}/initrd.img" "${work}/systemd-bootx64.efi"
}

make_root_image() {
	log "packing the root filesystem (${root_mib} MiB)"
	local img=${work}/feldcore-root.img
	rm -f "${img}"
	truncate -s "${root_mib}M" "${img}"
	mkfs.ext4 -F -L feldcore-a -E root_owner=0:0 "${img}"
	mkdir -p "${mnt}"
	sudo mount -o loop "${img}" "${mnt}"
	track_mount "${mnt}"
	sudo rsync -aHAX --numeric-ids \
		--exclude=/proc/** --exclude=/sys/** --exclude=/dev/** \
		--exclude=/run/** --exclude=/tmp/** \
		"${rootfs}/" "${mnt}/"
	sudo mkdir -p "${mnt}/proc" "${mnt}/sys" "${mnt}/dev" "${mnt}/run" "${mnt}/tmp"
	sudo chmod 1777 "${mnt}/tmp"
	sudo umount "${mnt}"
	mounts=()
	zstd -f -T0 -10 "${img}" -o "${work}/feldcore-root.img.zst"
}

write_entry() {
	local dest=$1 slot=$2 tries=$3 extra=${4:-}
	local name
	if [[ ${tries} -gt 0 ]]; then
		name="feldcore-${slot}+${tries}.conf"
	else
		name="feldcore-${slot}.conf"
	fi
	cat >"${dest}/loader/entries/${name}" <<EOF
title FELDCORE ${version}
version ${version}
sort-key feldcore
linux /feldcore/${slot}/vmlinuz
initrd /feldcore/${slot}/initrd.img
options root=LABEL=feldcore-${slot} ro console=tty0 console=ttyS0,115200 feldcore.slot=${slot} feldcore.version=${version}${extra}
EOF
}

# The build host may not be able to mount FAT. mtools writes the ESP directly.
fill_esp() {
	local fat=$1 slot=$2 extra=${3:-}
	local staging
	staging=$(mktemp -d)
	mkdir -p "${staging}/EFI/BOOT" "${staging}/EFI/systemd" "${staging}/feldcore/${slot}" "${staging}/loader/entries"
	cp "${work}/systemd-bootx64.efi" "${staging}/EFI/BOOT/BOOTX64.EFI"
	cp "${work}/systemd-bootx64.efi" "${staging}/EFI/systemd/systemd-bootx64.efi"
	cp "${work}/vmlinuz" "${staging}/feldcore/${slot}/vmlinuz"
	cp "${work}/initrd.img" "${staging}/feldcore/${slot}/initrd.img"
	write_entry "${staging}" "${slot}" 3 "${extra}"
	cat >"${staging}/loader/loader.conf" <<EOF
timeout 3
console-mode max
editor no
auto-entries no
auto-firmware no
EOF
	sudo mcopy -s -i "${fat}" "${staging}/EFI" "${staging}/feldcore" "${staging}/loader" ::/
	rm -rf "${staging}"
}

loop_disk() {
	LOOP=$(sudo losetup -fP --show "$1")
	track_loop "${LOOP}"
	sudo partx -u "${LOOP}" || true
	sleep 0.5
}

assemble_appliance() {
	log "assembling the appliance disk"
	local disk=${work}/feldcore-os.raw
	local slot_mib=$((root_mib + slot_slack))
	local total=$((16 + esp_mib + slot_mib + slot_mib + state_mib))
	rm -f "${disk}"
	truncate -s "${total}M" "${disk}"
	sgdisk -o \
		-n "1:1M:+${esp_mib}M" -t 1:EF00 -c 1:FC-ESP \
		-n "2:0:+${slot_mib}M" -t 2:8300 -c 2:feldcore-a \
		-n "3:0:+${slot_mib}M" -t 3:8300 -c 3:feldcore-b \
		-n "4:0:+${state_mib}M" -t 4:8300 -c 4:feldcore-state \
		"${disk}" >/dev/null
	loop_disk "${disk}"
	local loop=${LOOP}
	sudo mkfs.vfat -F 32 -n FC-ESP "${loop}p1"
	sudo mkfs.ext4 -F -L feldcore-b "${loop}p3" >/dev/null
	sudo mkfs.ext4 -F -L feldcore-state "${loop}p4" >/dev/null
	log "writing slot A"
	sudo dd if="${work}/feldcore-root.img" of="${loop}p2" bs=4M conv=fsync status=none
	fill_esp "${loop}p1" a ""
	sudo losetup -d "${loop}"
	loops=()
	zstd -f -T0 -10 "${disk}" -o "${work}/feldcore-os.raw.zst"
}

installer_overlay() {
	log "preparing the installer root"
	local img=${work}/feldcore-installer-root.img
	rm -f "${img}"
	cp --sparse=always "${work}/feldcore-root.img" "${img}"
	mkdir -p "${mnt}"
	sudo mount -o loop "${img}" "${mnt}"
	track_mount "${mnt}"
	sudo mkdir -p "${mnt}/etc/systemd/system/serial-getty@ttyS0.service.d"
	sudo tee "${mnt}/etc/systemd/system/serial-getty@ttyS0.service.d/autologin.conf" >/dev/null <<EOF
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I 115200,38400,9600
EOF
	sudo tee "${mnt}/etc/motd" >/dev/null <<EOF
FELDCORE installer.

This image does not install itself onto the disk it booted from.
To write the appliance onto another whole disk:

  feldcore-install --yes /dev/disk

The command refuses the booted disk and any disk with a mounted partition.
EOF
	sudo umount "${mnt}"
	mounts=()
}

assemble_installer() {
	log "assembling the installer disk"
	installer_overlay
	local disk=${work}/feldcore-installer.raw
	local slot_mib=$((root_mib + slot_slack))
	local payload_mib=2048
	local total=$((16 + esp_mib + slot_mib + payload_mib))
	rm -f "${disk}"
	truncate -s "${total}M" "${disk}"
	sgdisk -o \
		-n "1:1M:+${esp_mib}M" -t 1:EF00 -c 1:FC-ESP \
		-n "2:0:+${slot_mib}M" -t 2:8300 -c 2:feldcore-a \
		-n "3:0:+${payload_mib}M" -t 3:8300 -c 3:feldcore-payload \
		"${disk}" >/dev/null
	loop_disk "${disk}"
	local loop=${LOOP}
	sudo mkfs.vfat -F 32 -n FC-ESP "${loop}p1"
	sudo mkfs.ext4 -F -L feldcore-payload "${loop}p3" >/dev/null
	log "writing the installer root"
	sudo dd if="${work}/feldcore-installer-root.img" of="${loop}p2" bs=4M conv=fsync status=none
	mkdir -p "${mnt}" "${work}/payload-src"
	printf '%s\n' "${version}" >"${work}/payload-src/version"
	printf '%s\n' "${root_mib}" >"${work}/payload-src/root-mib"
	printf '%s\n' "${state_mib}" >"${work}/payload-src/state-mib"
	cp "${work}/feldcore-root.img.zst" "${work}/vmlinuz" "${work}/initrd.img" "${work}/systemd-bootx64.efi" "${work}/payload-src/"
	sudo mount "${loop}p3" "${mnt}"
	track_mount "${mnt}"
	sudo cp "${work}/payload-src/"* "${mnt}/"
	sudo umount "${mnt}"
	mounts=()
	fill_esp "${loop}p1" a ""
	sudo losetup -d "${loop}"
	loops=()
	zstd -f -T0 -10 "${disk}" -o "${work}/feldcore-installer.raw.zst"
}

publish() {
	log "signing the release"
	mkdir -p "${out}"
	cp "${work}/feldcore-root.img.zst" "${work}/vmlinuz" "${work}/initrd.img" "${out}/"
	cp "${work}/feldcore-os.raw.zst" "${work}/feldcore-installer.raw.zst" "${out}/"
	cp "${work}/keys/update.pub" "${out}/update.pub"
	local notes=${work}/NOTES
	cat >"${notes}" <<EOF
FELDCORE OS ${version}

Immutable Debian image. systemd-boot, UEFI only, Secure Boot off.
Slot A is the first image. Slot B is empty until an update is staged.
State is on the partition labeled feldcore-state, mounted at /var/lib/feldcore.
EOF
	local kver
	kver=$(sudo ls "${rootfs}/lib/modules" | head -1 || true)
	(cd "${repo}" && go run ./cmd/osmanifest manifest \
		-dir "${out}" -version "${version}" -notes-file "${notes}" \
		-kernel "${kver}" -channel stable -out "${out}/feldcore-os.json")
	(cd "${repo}" && go run ./cmd/osmanifest sign -key "${FELDCORE_UPDATE_KEY}" -in "${out}/feldcore-os.json")
	rm -f "${out}/"*.key "${out}/"*.sec
	if [[ -f ${out}/update.key || -f ${out}/feldcore-os.key ]]; then
		die "a private key landed in the output directory"
	fi
	log "output is ${out}"
}

configure_containerd() {
	# Debian ships a short config that leaves root at the compiled-in
	# /var/lib/containerd. The full default is rewritten so images and
	# snapshots land on the state partition.
	sudo mkdir -p "${rootfs}/etc/containerd"
	sudo chroot "${rootfs}" containerd config default | sudo tee "${rootfs}/etc/containerd/config.toml" >/dev/null
	sudo sed -i \
		-e 's#root = "/var/lib/containerd"#root = "/var/lib/feldcore/containerd"#' \
		-e 's/SystemdCgroup = false/SystemdCgroup = true/' \
		"${rootfs}/etc/containerd/config.toml"
	grep -q '/var/lib/feldcore/containerd' "${rootfs}/etc/containerd/config.toml" || die "containerd root was not moved onto the state partition"
}

install_cloudflared() {
	local sum ver tmp
	read -r sum ver < <(awk 'NF && $1 !~ /^#/ {print $1, $2; exit}' "${repo}/os/cloudflared.sha256")
	[[ -n ${sum} && -n ${ver} ]] || die "os/cloudflared.sha256 is empty"
	tmp=$(mktemp)
	curl -fsSL -o "${tmp}" "https://github.com/cloudflare/cloudflared/releases/download/${ver}/cloudflared-linux-amd64"
	echo "${sum}  ${tmp}" | sha256sum -c -
	sudo install -m 755 "${tmp}" "${rootfs}/usr/bin/cloudflared"
	rm -f "${tmp}"
}

# install_caddy verifies the pinned official release, then builds that same
# release with the Cloudflare DNS module. The stock binary cannot issue a
# public certificate. The result is a static program at /usr/bin/caddy.
# feldcored supervises it. The appliance does not run feldcore-caddy.
install_caddy() {
	local sum ver xcaddy plugin tmp src out
	read -r sum ver xcaddy plugin < <(awk 'NF && $1 !~ /^#/ {print $1, $2, $3, $4; exit}' "${repo}/os/caddy.sha256")
	[[ -n ${sum} && -n ${ver} && -n ${xcaddy} && -n ${plugin} ]] || die "os/caddy.sha256 is empty"
	tmp=$(mktemp)
	curl -fsSL -o "${tmp}" "https://github.com/caddyserver/caddy/releases/download/v${ver}/caddy_${ver}_linux_amd64.tar.gz"
	echo "${sum}  ${tmp}" | sha256sum -c -
	rm -f "${tmp}"
	src=$(mktemp -d)
	out="${src}/caddy"
	# Build outside the FELDCORE module so xcaddy's own module is used.
	(
		cd "${src}"
		GOBIN="${src}/bin" CGO_ENABLED=0 go install "github.com/caddyserver/xcaddy/cmd/xcaddy@${xcaddy}"
		CGO_ENABLED=0 "${src}/bin/xcaddy" build "v${ver}" \
			--with "github.com/caddy-dns/cloudflare@${plugin}" \
			--output "${out}"
	)
	"${out}" version | grep -q "v${ver}" || die "caddy build is not v${ver}"
	"${out}" list-modules | grep -q 'dns.providers.cloudflare' || die "caddy build is missing the Cloudflare DNS module"
	if ldd "${out}" >/dev/null 2>&1; then
		die "caddy build is dynamically linked"
	fi
	sudo install -m 755 "${out}" "${rootfs}/usr/bin/caddy"
	rm -rf "${src}"
}

build_sysexts() {
	local ids=${FELDCORE_BUILD_SYSEXTS-libvirt nvidia-container-toolkit}
	local id
	[[ -n ${ids} ]] || return 0
	command -v mksquashfs >/dev/null 2>&1 || die "missing command: mksquashfs"
	mkdir -p "${out}/sysexts"
	for id in ${ids}; do
		log "building sysext ${id}"
		bash "${repo}/os/sysext/build-sysext.sh" "${id}" "${rootfs}" "${out}/sysexts/${id}.raw"
	done
}

require debootstrap mkfs.ext4 mkfs.vfat sgdisk zstd rsync losetup go npm mcopy curl sha256sum
sudo mkdir -p "${work}"
sudo chown "$(id -u):$(id -g)" "${work}"
mkdir -p "${out}"
case ${builder} in
mkosi) mkosi_root ;;
debootstrap) debootstrap_root ;;
*) die "unknown FELDCORE_IMAGE_BUILDER=${builder}" ;;
esac
install_packages
build_binary
signing_key
apply_image
log "smoke test"
bash "${repo}/os/smoke.sh" --root "${rootfs}"
kernel_files
make_root_image
assemble_appliance
assemble_installer
build_sysexts
publish
log "done"

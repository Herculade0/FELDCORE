#!/bin/bash
# Installer ISO assembly. os/build.sh sources this file.
# work, repo, mirror, out, log, and die are provided by that script.
# shellcheck disable=SC2154
# Running it directly does nothing useful: the helpers live in the build script.
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
	echo "os/assemble-iso.sh is sourced by os/build.sh" >&2
	exit 1
fi

live_packages() {
	cat <<'EOF'
systemd
systemd-sysv
udev
linux-image-amd64
bash
coreutils
util-linux
mount
kmod
e2fsprogs
dosfstools
gdisk
parted
zstd
jq
ca-certificates
pciutils
firmware-linux-free
firmware-misc-nonfree
initramfs-tools
login
EOF
}

live_pkg_line() {
	live_packages | tr '\n' ' ' | sed 's/[[:space:]]*$//'
}

live_bind() {
	local live=$1
	sudo mkdir -p "${live}/proc" "${live}/sys" "${live}/dev" "${live}/dev/pts" "${live}/run"
	if ! mountpoint -q "${live}/proc"; then
		sudo mount --bind /proc "${live}/proc"
		track_mount "${live}/proc"
	fi
	if ! mountpoint -q "${live}/sys"; then
		sudo mount --bind /sys "${live}/sys"
		track_mount "${live}/sys"
	fi
	if ! mountpoint -q "${live}/dev"; then
		sudo mount --bind /dev "${live}/dev"
		track_mount "${live}/dev"
	fi
	if ! mountpoint -q "${live}/dev/pts"; then
		sudo mount --bind /dev/pts "${live}/dev/pts"
		track_mount "${live}/dev/pts"
	fi
}

live_unbind() {
	local live=$1
	detach_mount "${live}/dev/pts"
	detach_mount "${live}/dev"
	detach_mount "${live}/sys"
	detach_mount "${live}/proc"
	detach_mount "${live}/run"
}

mask_if_present() {
	local live=$1 unit=$2
	if [[ -e ${live}/usr/lib/systemd/system/${unit} || -e ${live}/lib/systemd/system/${unit} ]]; then
		sudo mkdir -p "${live}/etc/systemd/system"
		sudo ln -sfn /dev/null "${live}/etc/systemd/system/${unit}"
	fi
}

# xorriso 1.5.6 rejects -appended_gpt_with_gaps, and it rejects -hfsplus next
# to a GPT unless -apm-block-size is 2048. -hfs-bless-by also needs a bless
# type. Try the documented command, then the flags this xorriso accepts, then
# a GPT plus El Torito image with no HFS+ volume. The HFS+ file names stay in
# the ISO tree either way.
iso_xorriso() {
	local mode=$1
	shift
	rm -f "${iso}"
	log "xorriso layout ${mode}"
	if xorriso "$@" >"${work}/xorriso-${mode}.log" 2>&1; then
		printf '%s\n' "${mode}" >"${work}/feldcore-installer.iso.layout"
		log "xorriso layout ${mode} wrote the ISO"
		return 0
	fi
	log "xorriso layout ${mode} was rejected"
	tail -n 30 "${work}/xorriso-${mode}.log" >&2 || true
	rm -f "${iso}"
	return 1
}

assert_installer_iso() {
	local iso=$1 efiboot=$2
	local sig hdr got want gpt label report
	sig=$(dd if="${iso}" bs=1 skip=510 count=2 status=none | od -An -tx1 | tr -d ' \n')
	[[ ${sig} == 55aa ]] || die "installer ISO has no MBR signature (got ${sig})"
	hdr=$(dd if="${iso}" bs=1 skip=512 count=8 status=none)
	[[ ${hdr} == "EFI PART" ]] || die "installer ISO has no GPT header"
	got=$(dd if="${iso}" bs=1 skip=$((32768 + 40)) count=32 status=none)
	want=$(printf '%-32s' FELDCORE)
	[[ ${got} == "${want}" ]] || die "installer ISO volume id is not FELDCORE"
	gpt=$(sgdisk -p "${iso}" 2>&1 || true)
	printf '%s\n' "${gpt}" | grep -q EF00 || die "installer ISO GPT has no EF00 partition"
	if printf '%s\n' "${gpt}" | grep -q 'FC-ESP'; then
		die "installer ISO must not contain a partition named FC-ESP"
	fi
	label=$(mlabel -i "${efiboot}" -s 2>&1 || mlabel -i "${efiboot}" :: 2>&1 || true)
	printf '%s\n' "${label}" | grep -q 'FC-ISOEFI' || die "efiboot label is not FC-ISOEFI (${label})"
	if printf '%s\n' "${label}" | grep -q 'FC-ESP'; then
		die "efiboot label must not be FC-ESP"
	fi
	report=$(xorriso -indev "${iso}" -report_el_torito plain 2>&1 || true)
	printf '%s\n' "${report}" | grep -q 'UEFI' || die "installer ISO has no UEFI El Torito image"
	log "installer ISO checks passed ($(cat "${work}/feldcore-installer.iso.layout"))"
}

assemble_iso() {
	command -v xorriso >/dev/null 2>&1 || die "missing command: xorriso"
	command -v grub-mkimage >/dev/null 2>&1 || die "missing command: grub-mkimage"
	command -v mksquashfs >/dev/null 2>&1 || die "missing command: mksquashfs"
	command -v mkfs.vfat >/dev/null 2>&1 || die "missing command: mkfs.vfat"
	command -v mcopy >/dev/null 2>&1 || die "missing command: mcopy"
	local payload=${work}/payload-src
	local part
	[[ -d ${payload} ]] || die "assemble_iso: ${payload} does not exist"
	for part in feldcore-root.img.zst vmlinuz initrd.img systemd-bootx64.efi version root-mib state-mib; do
		[[ -f ${payload}/${part} ]] || die "assemble_iso: payload is missing ${part}"
	done

	local live=${work}/live-root
	local stamp=${live}/etc/feldcore-live-stamp
	local want
	want=$(live_pkg_line)
	sudo mkdir -p "${work}"
	sudo chown "$(id -u):$(id -g)" "${work}"
	if [[ ! -f ${live}/etc/debian_version || -d ${live}/debootstrap ]]; then
		log "debootstrap live installer root"
		sudo mkdir -p "${live}"
		sudo debootstrap --variant=minbase trixie "${live}" "${mirror}"
	else
		log "reusing live installer root"
	fi
	live_bind "${live}"
	local have=""
	if [[ -f ${stamp} ]]; then
		have=$(sudo cat "${stamp}" || true)
	fi
	if [[ ${have} != "${want}" || ${FELDCORE_OS_REPACKAGES:-0} == 1 ]]; then
		log "installing live installer packages"
		sudo cp /etc/resolv.conf "${live}/etc/resolv.conf"
		printf 'deb %s trixie main non-free-firmware\ndeb http://deb.debian.org/debian-security trixie-security main non-free-firmware\ndeb %s trixie-updates main non-free-firmware\n' \
			"${mirror}" "${mirror}" | sudo tee "${live}/etc/apt/sources.list" >/dev/null
		printf '#!/bin/sh\nexit 101\n' | sudo tee "${live}/usr/sbin/policy-rc.d" >/dev/null
		sudo chmod 755 "${live}/usr/sbin/policy-rc.d"
		printf 'APT::Install-Recommends "false";\nAPT::Install-Suggests "false";\n' |
			sudo tee "${live}/etc/apt/apt.conf.d/00no-recommends" >/dev/null
		local -a pkgs=()
		mapfile -t pkgs < <(live_packages)
		sudo chroot "${live}" apt-get update
		sudo chroot "${live}" env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${pkgs[@]}"
		sudo chroot "${live}" apt-get clean
		printf '%s\n' "${want}" | sudo tee "${stamp}" >/dev/null
	else
		log "live installer packages already installed"
	fi
	sudo rm -f "${live}/etc/resolv.conf" "${live}/usr/sbin/policy-rc.d"
	sudo cp -a "${repo}/os/live/." "${live}/"
	sudo chmod 755 \
		"${live}/usr/share/initramfs-tools/hooks/feldcore" \
		"${live}/usr/share/initramfs-tools/scripts/feldcore"
	sudo mkdir -p "${live}/usr/sbin" "${live}/usr/lib/systemd/system" \
		"${live}/etc/systemd/system/multi-user.target.wants" "${live}/payload"
	sudo cp "${repo}/os/image/usr/sbin/feldcore-install" "${live}/usr/sbin/feldcore-install"
	sudo chmod 755 "${live}/usr/sbin/feldcore-install"
	sudo cp "${repo}/os/image/usr/lib/systemd/system/feldcore-install.service" \
		"${live}/usr/lib/systemd/system/feldcore-install.service"
	sudo ln -sfn /usr/lib/systemd/system/feldcore-install.service \
		"${live}/etc/systemd/system/multi-user.target.wants/feldcore-install.service"
	sudo truncate -s 0 "${live}/etc/machine-id"
	sudo chmod 644 "${live}/etc/machine-id"
	sudo rm -f "${live}/etc/securetty"
	sudo chroot "${live}" passwd -d root
	mask_if_present "${live}" systemd-networkd-wait-online.service
	mask_if_present "${live}" apt-daily.timer
	mask_if_present "${live}" apt-daily-upgrade.timer
	log "building the live initrd"
	sudo chroot "${live}" update-initramfs -u
	live_unbind "${live}"
	if mountpoint -q "${live}/proc" || mountpoint -q "${live}/sys" || mountpoint -q "${live}/dev"; then
		die "live root still has a host mount"
	fi

	local ver kver tree efiboot
	ver=$(tr -d ' \n' <"${payload}/version")
	[[ -n ${ver} ]] || die "payload version is empty"
	kver=$(sudo ls "${live}/lib/modules" | sort -V | tail -n 1)
	[[ -n ${kver} && -f ${live}/boot/vmlinuz-${kver} && -f ${live}/boot/initrd.img-${kver} ]] || die "live kernel was not built"
	tree=${work}/iso
	rm -rf "${tree}"
	mkdir -p "${tree}/boot/grub" "${tree}/EFI/BOOT" "${tree}/feldcore" "${tree}/live" \
		"${tree}/payload" "${tree}/System/Library/CoreServices"
	sudo cp "${live}/boot/vmlinuz-${kver}" "${tree}/boot/vmlinuz"
	sudo cp "${live}/boot/initrd.img-${kver}" "${tree}/boot/initrd.img"
	log "packing the live filesystem"
	sudo mksquashfs "${live}" "${tree}/live/filesystem.squashfs" \
		-comp zstd -Xcompression-level 15 -b 1M -noappend -wildcards \
		-e 'boot/vmlinuz-*' 'boot/initrd.img-*' var/cache/apt var/lib/apt/lists
	cp "${payload}/feldcore-root.img.zst" "${payload}/vmlinuz" "${payload}/initrd.img" \
		"${payload}/systemd-bootx64.efi" "${payload}/version" "${payload}/root-mib" \
		"${payload}/state-mib" "${tree}/payload/"
	printf '%s\n' "${ver}" >"${tree}/feldcore/installer.marker"
	cp "${repo}/os/iso/early.cfg" "${tree}/boot/grub/early.cfg"
	local line
	: >"${tree}/boot/grub/grub.cfg"
	while IFS= read -r line || [[ -n ${line} ]]; do
		printf '%s\n' "${line//@VERSION@/${ver}}"
	done <"${repo}/os/iso/grub.cfg" >"${tree}/boot/grub/grub.cfg"
	log "building the installer EFI binary"
	grub-mkimage -O x86_64-efi \
		-o "${work}/BOOTX64.EFI" \
		-p /boot/grub \
		-c "${repo}/os/iso/early.cfg" \
		normal search search_fs_file search_label \
		part_gpt part_msdos fat iso9660 loopback \
		linux configfile echo reboot
	cp "${work}/BOOTX64.EFI" "${tree}/EFI/BOOT/BOOTX64.EFI"
	cp "${work}/BOOTX64.EFI" "${tree}/System/Library/CoreServices/boot.efi"
	local xml=${ver//&/&amp;}
	xml=${xml//</&lt;}
	xml=${xml//>/&gt;}
	cat >"${tree}/System/Library/CoreServices/SystemVersion.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProductName</key>
	<string>FELDCORE</string>
	<key>ProductVersion</key>
	<string>${xml}</string>
</dict>
</plist>
EOF
	: >"${tree}/mach_kernel"
	sudo chown -R "$(id -u):$(id -g)" "${tree}" "${work}/BOOTX64.EFI"

	local initlist
	initlist=$(lsinitramfs "${tree}/boot/initrd.img")
	printf '%s\n' "${initlist}" | grep -q 'scripts/feldcore' || die "live initrd is missing scripts/feldcore"
	printf '%s\n' "${initlist}" | grep -q 'losetup' || die "live initrd is missing losetup"
	printf '%s\n' "${initlist}" | grep -q 'blkid' || die "live initrd is missing blkid"

	efiboot=${work}/efiboot.img
	rm -f "${efiboot}"
	truncate -s 64M "${efiboot}"
	mkfs.vfat -F 32 -n FC-ISOEFI "${efiboot}"
	mmd -i "${efiboot}" ::/EFI
	mmd -i "${efiboot}" ::/EFI/BOOT
	mcopy -i "${efiboot}" "${work}/BOOTX64.EFI" ::/EFI/BOOT/BOOTX64.EFI
	mcopy -i "${efiboot}" "${repo}/os/iso/early.cfg" ::/early.cfg
	# mdir prints the 8.3 name as "BOOTX64  EFI", so match the path, not that text.
	mdir -i "${efiboot}" ::/EFI/BOOT/BOOTX64.EFI >/dev/null || die "efiboot image is missing BOOTX64.EFI"
	mdir -i "${efiboot}" ::/early.cfg >/dev/null || die "efiboot image is missing early.cfg"

	iso=${work}/feldcore-installer.iso
	local -a xor_args=(
		-as mkisofs
		-iso-level 3
		-r -J -joliet-long
		-V FELDCORE
		-o "${iso}"
		-partition_offset 16
		-append_partition 2 0xef "${efiboot}"
		-appended_part_as_gpt
		-e --interval:appended_partition_2:all::
		-no-emul-boot
		-c /boot.catalog
	)
	local bless=/System/Library/CoreServices/boot.efi
	local layout=${FELDCORE_ISO_LAYOUT:-auto}
	case ${layout} in
	auto)
		iso_xorriso doc "${xor_args[@]}" \
			-appended_gpt_with_gaps \
			-hfsplus \
			-hfsplus-file-creator-type chrp tbxj "${bless}" \
			-hfs-bless-by "${bless}" \
			"${tree}" \
			|| iso_xorriso hfs "${xor_args[@]}" \
				-apm-block-size 2048 \
				-hfsplus \
				-hfsplus-file-creator-type chrp tbxj "${bless}" \
				-hfs-bless-by i "${bless}" \
				"${tree}" \
			|| iso_xorriso gpt "${xor_args[@]}" "${tree}" \
			|| die "xorriso could not write the installer ISO"
		;;
	doc)
		iso_xorriso doc "${xor_args[@]}" \
			-appended_gpt_with_gaps \
			-hfsplus \
			-hfsplus-file-creator-type chrp tbxj "${bless}" \
			-hfs-bless-by "${bless}" \
			"${tree}" \
			|| die "xorriso rejected the documented command"
		;;
	hfs)
		iso_xorriso hfs "${xor_args[@]}" \
			-apm-block-size 2048 \
			-hfsplus \
			-hfsplus-file-creator-type chrp tbxj "${bless}" \
			-hfs-bless-by i "${bless}" \
			"${tree}" \
			|| die "xorriso rejected the HFS+ command"
		;;
	gpt)
		iso_xorriso gpt "${xor_args[@]}" "${tree}" || die "xorriso rejected the GPT command"
		;;
	*)
		die "unknown FELDCORE_ISO_LAYOUT=${layout}"
		;;
	esac
	assert_installer_iso "${iso}" "${efiboot}"
	log "feldcore-installer.iso is $(stat -c %s "${iso}") bytes"
}

# Publish the ISO, or its zstd wrapper when the ISO itself is at or over 2 GiB.
# The signed bytes are the asset that is copied into the output directory.
# The file is not added to feldcore-os.json.
publish_installer_iso() {
	local iso=${work}/feldcore-installer.iso
	[[ -f ${iso} ]] || die "the installer ISO was not built"
	local limit=$((2 * 1024 * 1024 * 1024))
	local size zsize asset
	size=$(stat -c %s "${iso}")
	rm -f \
		"${out}/feldcore-installer.iso" \
		"${out}/feldcore-installer.iso.minisig" \
		"${out}/feldcore-installer.iso.zst" \
		"${out}/feldcore-installer.iso.zst.minisig"
	if ((size < limit)); then
		cp "${iso}" "${out}/feldcore-installer.iso"
		asset=${out}/feldcore-installer.iso
	else
		log "feldcore-installer.iso is ${size} bytes; compressing the release asset"
		zstd -f -T0 -10 "${iso}" -o "${work}/feldcore-installer.iso.zst"
		zsize=$(stat -c %s "${work}/feldcore-installer.iso.zst")
		if ((zsize < limit)); then
			cp "${work}/feldcore-installer.iso.zst" "${out}/feldcore-installer.iso.zst"
			asset=${out}/feldcore-installer.iso.zst
		else
			die "feldcore-installer.iso is ${size} bytes and feldcore-installer.iso.zst is ${zsize} bytes; both are at or over 2 GiB"
		fi
	fi
	[[ -n ${FELDCORE_UPDATE_KEY:-} && -f ${FELDCORE_UPDATE_KEY} ]] || die "FELDCORE_UPDATE_KEY is not set"
	(cd "${repo}" && go run ./cmd/osmanifest sign-file -key "${FELDCORE_UPDATE_KEY}" -in "${asset}")
	log "signed $(basename "${asset}")"
}

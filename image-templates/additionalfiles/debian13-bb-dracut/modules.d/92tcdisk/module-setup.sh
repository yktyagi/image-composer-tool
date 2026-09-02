#!/bin/sh

check() {
    # TODO: add ntfs-3g
    # require_binaries ntfs-3g || { derror "tcdisk: ntfs-3g is required"; return 1; }

    for cmd in awk blkid cat grep insmod mkdir modprobe mount sleep tr umount
    do
        require_binaries "$cmd" || { derror "tcdisk: $cmd is required"; return 255; }
    done
    unset cmd

    [ -s "$moddir/tc-client" ] || { derror "tc-client not found"; return 255; }
    [ -s "$moddir/tcdisk.sh" ] || { derror "tcdisk.sh not found"; return 255; }
    return 0
}

depends() {
    echo "kernel-modules"
}

installkernel() {
    instmods nbd
}

install() {
    # TODO: add ntfs-3g
    # inst_binary ntfs-3g "/usr/bin/@tfs-3g" || { derror "tcdisk: failed to install binary ntfs-3g"; return 1; }

    for cmd in awk blkid cat grep insmod mkdir modprobe mount sleep tr umount
    do
        inst_binary "$cmd" || { derror "tcdisk: failed to install binary $cmd"; return 1; }
    done
    unset cmd

    inst_binary "$moddir/tc-client" "/usr/bin/tc-client" || { derror "tcdisk: failed to install binary tc-client"; return 1; }
    chmod 0755 "$initdir/usr/bin/tc-client" || { derror "tcdisk: failed to chmod tc-client"; return 1; }

    inst_hook initqueue 92 "$moddir/tcdisk.sh" || { derror "tcdisk: failed to install initqueue hook tcdisk.sh"; return 1; }
    chmod 0755 "$initdir/lib/dracut/hooks/initqueue/92-tcdisk.sh" || { derror "tcdisk: failed to chmod tcdisk.sh"; return 1; }
}

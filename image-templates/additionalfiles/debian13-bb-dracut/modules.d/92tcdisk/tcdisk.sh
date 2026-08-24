#!/bin/sh

g_count=1

print_msg() {
    echo "tcdisk: $@" >/dev/kmsg
    echo "tcdisk: $@" >/dev/tty1
}

print_msg "pre-mount nbd disk for UCC"

if ! mkdir /tmp/tcdisk ; then
    if [ -d /tmp/tcdisk ] ; then
        print_msg "/tmp/tcdisk exists"
        return 0
    fi
    print_msg "failed to create /tmp/tcdisk"
    return 1
fi

modprobe nbd
if [ ! -d /sys/module/nbd ] ; then
    print_msg "fail to load nbd kernel module"
    return 1
fi

until [ $g_count -gt 3 ] ; do
    parts=`blkid | awk -F':' '{print $1}' | tr '\n' ' '`
    print_msg "round #$g_count parts: $parts"

    for partition in $parts; do
        fstype=`blkid -s TYPE $partition | awk -F'"' '{print $2}'`

        if [ x"$fstype" = x"ntfs" ]; then
            @tfs-3g $partition /tmp/tcdisk
        else
            mount $partition /tmp/tcdisk
        fi

        if [ -f /tmp/tcdisk/osenv.inf ]; then
            print_msg "round #$g_count found boot file"
            break
        else
            umount /tmp/tcdisk
        fi
    done

    if [ -f /tmp/tcdisk/osenv.inf ] ; then
        print_msg "Round #$g_count break the loop"
        break
    fi

    g_count=$(( g_count + 1 ))

    sleep 1
done

if [ -f /tmp/tcdisk/osenv.inf ] ; then
    print_msg "UCC_IMAGE_BOOT"
    BASEFILE=`cat /tmp/tcdisk/osenv.inf | awk -F ':' '/BASEIMG/ {print $2}' | awk -F '\\' '{print $5}'`
    MDFFILE=`cat /tmp/tcdisk/osenv.inf | awk -F ':' '/MDFIMG/ {print $2}' | awk -F '\\' '{print $5}'`
    UDFFILE=`cat /tmp/tcdisk/osenv.inf | awk -F ':' '/UDFIMG/ {print $2}' | awk -F '\\' '{print $5}'`
    print_msg "tc-client /tmp/tcdisk/$BASEFILE /tmp/tcdisk/$MDFFILE /tmp/tcdisk/$UDFFILE"
    echo $BASEFILE | grep -i "\.qcow2.img" && qcow2img=1 || qcow2img=0
    if [ $qcow2img -eq 1 ] ; then
         tc-client "#" /tmp/tcdisk/$BASEFILE /tmp/tcdisk/$UDFFILE &
         sleep 2
    else
         tc-client /tmp/tcdisk/$BASEFILE /tmp/tcdisk/$MDFFILE /tmp/tcdisk/$UDFFILE &
         sleep 2
    fi
else
    print_msg "HOST_NATIVE_BOOT"
fi

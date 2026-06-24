# Configure a Custom Script in the Initrd (Debian 13, GRUB)

## Overview

**Goal:** Run your own shell script **on the device during early boot** (inside the initramfs), for **Debian 13** **`imageType: raw`** images that use **GRUB**.

Debian raw images with GRUB build the booted initramfs with **`update-initramfs`** (**initramfs-tools**). You add a **hook** (pack your script into the initramfs at image build time) and an **`init-bottom`** script (run it during boot).

**Start from your image template:** add `systemConfig.additionalFiles` (and related entries) that point at files in the repo. This page shows the YAML first, then the file contents and boot stages.

**Full working example:** `image-templates/debian13-x86_64-bb-raw.yml` and `image-templates/additionalfiles/debian13-bb/`.

**Do not use** [`systemConfig.configurations`](configure-additional-actions-for-build.md) to *run* your script on the device—that only runs while the image is being built. You may still use `configurations` for `chmod` (shown below).

Copying a script only to `/usr/local/sbin` with `additionalFiles` does **not** run it in the initrd. You need the **initramfs-tools** hook and script paths below.

---

## Image template changes

Add the following under `systemConfig` (for example copy [`debian13-x86_64-minimal-raw.yml`](../../image-templates/debian13-x86_64-minimal-raw.yml) and extend it, or use [`debian13-x86_64-bb-raw.yml`](../../image-templates/debian13-x86_64-bb-raw.yml)).

Paths in **`local`** are relative to the **directory that contains your template YAML** (`image-templates/…` → `additionalfiles/debian13-bb/…`).

```yaml
systemConfig:
  name: bb

  bootloader:
    bootType: efi
    provider: grub

  packages:
    - initramfs-tools
    # … keep your other packages (see bb example)

  additionalFiles:
    - local: additionalfiles/debian13-bb/hello.sh
      final: /usr/local/sbin/hello.sh
    - local: additionalfiles/debian13-bb/hooks/hello
      final: /etc/initramfs-tools/hooks/hello
    - local: additionalfiles/debian13-bb/scripts/init-bottom/hello
      final: /etc/initramfs-tools/scripts/init-bottom/hello

  configurations:
    - cmd: "chmod 755 /usr/local/sbin/hello.sh /etc/initramfs-tools/hooks/hello /etc/initramfs-tools/scripts/init-bottom/hello"
```

### What each `additionalFiles` entry does

| `local` (your repo) | `final` (inside the built image) | Role |
|---------------------|----------------------------------|------|
| `…/hello.sh` | `/usr/local/sbin/hello.sh` | Your script on the rootfs; the **hook** copies it into the initramfs. |
| `…/hooks/hello` | `/etc/initramfs-tools/hooks/hello` | Runs when **`update-initramfs`** builds the initramfs (pack step). |
| `…/scripts/init-bottom/hello` | `/etc/initramfs-tools/scripts/init-bottom/hello` | Runs on the **device** during early boot (execute step). |

- **`local`:** file on the build host (must exist before compose).
- **`final`:** path on the image; ICT copies files here before GRUB install runs **`update-initramfs`**.

Rename `debian13-bb` in `local` paths to match your folder name. Keep the **`final`** paths as shown.

### Field summary

| Template field | Purpose |
|----------------|---------|
| `additionalFiles` | **Required.** Installs hook, boot script, and your `hello.sh`. |
| `configurations` | Optional `chmod` at build time. |
| `packages` | Include **`initramfs-tools`** (and **`grub-cloud-amd64`** or your GRUB packages). |
| `bootloader.provider` | **`grub`** for this guide (initramfs is rebuilt via `update-initramfs`). |
| `kernel.cmdline` | Optional: add `break=init` while debugging initramfs (noisy). |

---

## Build and check

**Build the tool, install prerequisites, validate, and compose the image** using the [README.md](../../README.md) (Quick Start and *Compose an Image*). Example template:

`image-templates/debian13-x86_64-bb-raw.yml`

Run validate if you use it (see [Usage Guide](./usage-guide.md)). If validate warns about a missing `local` file, fix the path or add the file under [Where to put files in the repo](#where-to-put-files-in-the-repo).

**On the device:**

1. Boot the flashed **raw** image.
2. Use serial console if your template sets `console=ttyS0,...` on the kernel cmdline.
3. Look for your message during early boot, or run: `dmesg | grep -i hello`

Optional on a machine with the image mounted: `lsinitramfs /boot/initrd.img-* | grep hello` to confirm the script was packed.

---

## Where to put files in the repo

| Layout | `local` in template |
|--------|---------------------|
| Next to templates (recommended) | `image-templates/additionalfiles/<your-name>/...` |
| Debian OS defaults tree | `../additionalfiles/...` from `config/osv/debian/debian13/imageconfigs/defaultconfigs/` |

Example tree:

```text
image-templates/
  debian13-x86_64-bb-raw.yml
  additionalfiles/debian13-bb/
    hello.sh
    hooks/hello
    scripts/init-bottom/hello
```

---

## Supporting files (content to create)

### `hello.sh`

```sh
#!/bin/sh
echo "hello from initrd (debian13-bb)" >/dev/kmsg
```

Use `/dev/kmsg` or `logger` so output appears on serial or in `dmesg`.

### `hooks/hello`

Runs during **`update-initramfs`** on the build machine; copies `hello.sh` into the initramfs image.

```sh
#!/bin/sh
PREREQ=""
prereqs() { echo "$PREREQ"; exit 0; }

case "$1" in
prereqs) prereqs; exit 0 ;;
esac

. /usr/share/initramfs-tools/hook-functions
copy_exec /usr/local/sbin/hello.sh /usr/local/sbin/hello.sh
```

### `scripts/init-bottom/hello`

Runs on the **device** in the initrd (default: late in initramfs, before switch to the installed system).

```sh
#!/bin/sh
PREREQ=""
prereqs() { echo "$PREREQ"; exit 0; }

case "$1" in
prereqs) prereqs; exit 0 ;;
esac

if [ -x /usr/local/sbin/hello.sh ]; then
	/usr/local/sbin/hello.sh
fi
```

Make all three executable (`chmod 755`) or use the `configurations` line in the template.

Every initramfs-tools hook and script must start with the **`PREREQ` / `prereqs`** block shown above.

---

## Choosing an initramfs-tools boot stage

The **hook** always runs at **image build** time when the initramfs is generated. To change **when your script runs on the device**, move the runner to a different directory under `scripts/` (and update the template `final:` path).

| `final` path under `/etc/initramfs-tools/scripts/` | When it runs (plain language) | Good for |
|----------------------------------------------------|-------------------------------|----------|
| **`init-bottom/hello`** | Late initrd, after root handling, before switch_root. **Default in the example.** | Logging, checks before the real OS starts. |
| `init-premount` | Early, before mounting root | Very early setup |
| `local-premount` | Before local root mount | Block device ready, root not mounted yet |
| `local-bottom` | After local root mount steps | Work that needs the root filesystem mounted in initrd |

There is no `99` prefix naming rule; the file name (`hello`) is arbitrary.

---

## Troubleshooting

| Problem | Check |
|---------|--------|
| No output on boot | All three `additionalFiles` entries; `chmod 755` on hook and scripts; `initramfs-tools` in packages. |
| Validate / build skips a file | Wrong `local` path relative to the template YAML. |
| Script on disk but not in initrd | Missing `hooks/hello` or hook not executable. |
| Script in initramfs but never runs | Missing or wrong `scripts/.../hello` path; wrong boot stage directory. |
| Wrong image type or bootloader | This guide targets **Debian 13 raw** with **`bootloader: grub`**. |

---

## Related documentation

- [Custom commands at image build time](configure-additional-actions-for-build.md) — not initrd execution on the device.
- [Image templates](../architecture/image-composer-tool-templates.md) — `additionalFiles` fields and merge behavior.
- [README.md](../../README.md) — build and compose commands.

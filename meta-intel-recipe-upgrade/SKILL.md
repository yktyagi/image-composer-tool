---
name: meta-intel-recipe-upgrade
description: Upgrade a recipe (or a dependency stack) in meta-intel / meta-openvino to a newer upstream version the user's way — respecting cross-recipe dependency order, clang15 constraints, per-recipe build + core-image-sato-sdk verification, and the user's commit/sign-off conventions. Use whenever the user asks to upgrade, bump, or update a meta-intel/meta-openvino recipe version.
---

# Upgrading a meta-intel / meta-openvino recipe

The user drives upgrades **one recipe at a time**. Do not upgrade a recipe (or
start a stack) until they explicitly name it. Never push. Never touch untracked
dependency trees (`bitbake/`, `openembedded-core/`, `meta-yocto/`, root `*.md` /
`*.sh`) — fixes belong in a layer the user owns (meta-intel, meta-qa,
meta-clang-revival, meta-openvino).

## Before you touch anything — read the recall notes

The gotchas that actually break these upgrades live in per-stack memory files.
Read the ones relevant to the recipe **before** editing:

- `meta-intel-recipe-upgrade-constraints` — dependency order + hard rules (READ FIRST)
- `meta-intel-qat-and-kernel-build-notes` — QAT/local.conf, kernel RT/Non-RT, dpcpp
- `rendering-stack-upgrade-notes` — ispc/embree/openvkl/ospray/oidn/rkcommon
- `levelzero-npu-lms-stack-upgrade-notes` — level-zero/npu-driver/lms/metee
- `kernel-stack-upgrade-notes` — linux-intel(-rt), ixgbe/ixgbevf/backport-iwlwifi
- `graphics-stack-upgrade-notes` — gmmlib/metrics-discovery/libva-intel
- `multimedia-stack-upgrade-notes` — media-driver/libvpl/vpl-gpu-rt/itt
- `microcode-boot-stack-upgrade-notes` — intel-microcode/iucode-tool/slimboot-tools
- `support-crypto-trace-stack-upgrade-notes` — metee/intel-crypto-mb/isa-l/cmt-cat
- `mesa-rusticl-build-notes`, `oe-core-*`, `license-spdx-expression-warning-sweep`

These are point-in-time; verify file:line against current code before relying on them.

## Hard constraints (from `meta-intel-recipe-upgrade-constraints`)

- **Dependency chains — upgrade the whole chain together, in order:**
  intel-graphics-compiler → level-zero → intel-compute-runtime;
  metee → lms; rkcommon → ospray; openvino-inference-engine → open-model-zoo;
  ispc → oidn. The rendering stack (rkcommon, ispc, embree, openvkl, ospray,
  oidn) rebuilds as a unit when any one moves.
- **clang15 only.** meta-intel recipes build with clang15 from
  meta-clang-revival (image builds latest clang for mesa + clang15 for
  meta-intel). When bumping igc/level-zero/compute-runtime/onednn/rkcommon/
  ispc/embree/openvkl/ospray, check for a **meta-clang-revival bbappend** too.
- **igc + compute-runtime: OFFICIAL releases only.** Others may use tags.
- **ispc is pinned** — newer ispc needs llvm > 15 which we can't provide, so oidn
  is bounded by the ispc version. Don't bump ispc.
- Upgrade even when the diff is only functional changes.

## Per-recipe procedure

1. **Check it's not already latest.** Use the upstream tracker:
   ```bash
   source openembedded-core/oe-init-build-env <builddir>
   export PYTHONPATH=/data/yogesht/yocto_master/bitbake/lib
   devtool check-upgrade-status <recipe>
   ```
   If the tracker is broken (`UNKNOWN_BROKEN`) confirm the real upstream tag with
   `git ls-remote --tags <url> | sed 's#.*refs/tags/##' | grep -v '\^{}'` and fix
   `UPSTREAM_CHECK_GITTAGREGEX` (git recipes) or `UPSTREAM_CHECK_URI` +
   `UPSTREAM_CHECK_REGEX` (tarball/HTML — match `releases/tag/vX.Y.Z`, not asset
   filenames). Use `RECIPE_NO_UPDATE_REASON` for genuinely untrackable recipes.

2. **Rename the recipe file** to the new version (`git mv old.bb new.bb`) — do
   NOT leave the old file. Bump `SRCREV` and refresh `SRC_URI[...sha256sum]` /
   `SRCREV` as the versioning scheme requires (git tag vs tarball vs floating).

3. **Check bundled deps** in `SRC_URI`: find what bundled-dep versions the new
   recipe needs and bump those too.

4. **Refresh/rebase or drop patches** against the new source. Drop patches now
   upstream; refresh line offsets on the rest.

5. **Build the recipe:**
   ```bash
   bitbake <recipe>
   ```
   For oneapi / QAT recipes prepend `no_proxy=""`. For RT kernel, flip the
   provider in local.conf (see kernel notes). Build the kernel before external
   kernel modules (ixgbe/ixgbevf/backport-iwlwifi).

6. **Verify the image isn't broken:**
   ```bash
   MACHINE=intel-skylake-64 DISTRO=poky bitbake core-image-sato-sdk
   ```

7. **Commit** — one recipe per commit (see conventions below). Then
   `git status` to confirm a clean tree.

8. **Record any new non-obvious gotcha** in the matching per-stack memory file
   (update, don't duplicate; add the one-line pointer to `MEMORY.md`).

## Commit conventions

One commit per recipe, ≤80-char message lines, SSH-signed + signed-off, NO
`Co-Authored-By` / tool-attribution trailer, NOT pushed. Heading + body format:

```
<recipe> : upgrade <oldver> -> <newver>

Release Notes:
<upstream release/tag URL>

Signed-off-by: Yogesh Tyagi <yogesh.tyagi@intel.com>
```

Do NOT add filler like "No patches are carried by this recipe." See the
`create-commit` skill for signing/sign-off mechanics.

## Cleanup

Remove any temp files/logs/scratch build dirs you created (`*.log`,
`build-checklayer`, `/tmp/*` scratch). Leave only the intended recipe change and
its commit. Run `git status` after each upgrade and after each commit.

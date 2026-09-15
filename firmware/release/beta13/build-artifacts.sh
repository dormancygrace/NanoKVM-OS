#!/bin/bash
# Current beta-13 distribution: full SD image only, no .nkos package.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
base=${NANOKVM_BUILD_BASE:?Set the accepted build-input root}
old=${NANOKVM_BETA10_OUTPUT:-$base/releases/beta10-seq23}
out=${NANOKVM_BETA13_OUTPUT:-$base/releases/beta13-seq29-r2}
fitdir=${NANOKVM_LED_FIT_OUTPUT:-$base/universal-boot-v2}
dest=${NANOKVM_RELEASE_OUTPUT:?Set a fresh release artifact directory}
epoch=${SOURCE_DATE_EPOCH:?Set the recorded release source epoch}
python3 "$repo/scripts/assemble-enhanced-test-image.py" \
  --layout-image "$old/base-beta9.img" --fit "$fitdir/detect/boot.sd" \
  --fip "$old/boot-files/fip.bin" --rootfs "$out/rootfs.ext4" \
  --kernel-output "$old/kernel-output" --board-stage "$old/board-stage" \
  --dumpimage "$base/buildroot-output/host/bin/dumpimage" --output "$out/image" \
  --fit-sha256 1b74553bca83d21eca18a6609bb0001639c52fc679d425cbcacca9b3c5bb7f63 \
  --fip-sha256 8254d38124e66877ab6c4a894f656d8ee6dc299d813dd06f31037ddaa2e51b9d \
  --epoch "$epoch" --version 1.0.0-beta.13
python3 - "$repo" "$out" "$dest" "$epoch" <<'PY'
from pathlib import Path
import hashlib,json,subprocess,sys,zipfile
repo,out,dest=map(Path,sys.argv[1:4]);sys.path.insert(0,str(repo/'scripts'))
from release_names import release_names
names=release_names('1.0.0-beta.13')
with zipfile.ZipFile(dest/names['image_zip'],'x',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as z:
    z.write(out/'image'/names['image'],names['image'])
p=dest/names['image_zip']
with p.open('rb') as stream: digest=hashlib.file_digest(stream,'sha256').hexdigest()
files={p.name:{'sha256':digest,'bytes':p.stat().st_size}}
(dest/names['checksums']).write_text(digest+'  '+p.name+'\n')
manifest={'version':'1.0.0-beta.13','sequence':29,'kernel':'7.2.5-nanokvm-os-r3',
          'distribution':'full-sd-image-only','source_date_epoch':int(sys.argv[4]),
          'source_commit':subprocess.check_output(['git','-C',str(repo),'rev-parse','HEAD'],text=True).strip(),
          'status':'prepared-not-published','complete_image_device_tested':False,'files':files,
          'rootfs_delta':json.loads((out/'rootfs-delta.json').read_text()),
          'prior_acceptance':'Automatic board selection fixtures pass; complete image device acceptance pending.',
          'hardware_scope':'Automatic alpha/beta/pcie/base selection; physical qualification recorded separately per board'}
(dest/'build-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print(json.dumps(files,indent=2))
PY
echo BETA13_IMAGE_ONLY_READY

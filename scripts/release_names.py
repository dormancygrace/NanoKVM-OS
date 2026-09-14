#!/usr/bin/env python3
"""Canonical names for NanoKVM OS releases and distributable files."""
import argparse, json, re

def release_names(version):
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)\.(0|[1-9][0-9]*))?',version):
        raise ValueError('Expected a SemVer version, optionally alpha.N, beta.N or rc.N')
    core,sep,prerelease=version.partition('-')
    display=core+('-'+prerelease.replace('.','-') if sep else '')
    stem='NanoKVM-OS-v'+display
    names=dict(version=version,tag='v'+version,title=stem,image=stem+'.img',image_zip=stem+'.img.zip',package=stem+'.nkos',checksums='SHA256SUMS')
    # Last compatibility release for beta-9's fixed-name update discovery.
    if version == '1.0.0-beta.10':
        names.update(image='NanoKVM-OS-'+version+'.img', image_zip='NanoKVM-OS-'+version+'.img.zip', package='NanoKVM-OS-update.nkos')
    return names

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version')
    args=parser.parse_args()
    print(json.dumps(release_names(args.version),indent=2))

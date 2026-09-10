#!/usr/bin/env python3
"""Reject kernel configurations that drop required NanoKVM board interfaces."""
import sys
import json
from pathlib import Path

config={}
for line in Path(sys.argv[1]).read_text().splitlines():
    if line.startswith('CONFIG_') and '=' in line:
        key,value=line.split('=',1);config[key[7:]]=value
required_builtin='''ARCH_SOPHGO ERRATA_THEAD ERRATA_THEAD_MAE ERRATA_THEAD_CMO ERRATA_THEAD_PMU ERRATA_THEAD_GHOSTWRITE RISCV_ISA_XTHEADVECTOR MODULES DEVTMPFS DEVTMPFS_MOUNT BLK_DEV_INITRD ION ION_CARVEOUT_HEAP ION_CVITEK NANOKVM_EFUSE NANOKVM_HDMI_RESET PINCTRL_SOPHGO_SG2002 CLK_SOPHGO_CV1800 GPIO_DWAPB GPIO_CDEV I2C_DESIGNWARE_PLATFORM I2C_CHARDEV I2C_GPIO SERIAL_8250_DW SERIAL_8250_CONSOLE SERIAL_EARLYCON_RISCV_SBI RISCV_SBI_V01 SOPHGO_CV1800_RTCSYS STMMAC_ETH DWMAC_SOPHGO MDIO_BUS_MUX MDIO_BUS_MUX_MMIOREG NANOKVM_CVITEK_PHY MMC MMC_SDHCI_OF_DWCMSHC PWRSEQ_SIMPLE USB_DWC2 USB_DWC2_DUAL_ROLE PHY_SOPHGO_CV1800_USB2 USB_GADGET USB_CONFIGFS USB_CONFIGFS_SERIAL USB_CONFIGFS_ACM USB_CONFIGFS_NCM USB_CONFIGFS_ECM USB_CONFIGFS_RNDIS USB_CONFIGFS_MASS_STORAGE USB_CONFIGFS_F_HID INPUT_EVDEV KEYBOARD_GPIO LEDS_GPIO CONFIGFS_FS EXT4_FS VFAT_FS EXFAT_FS TMPFS TUN'''.split()
required_builtin += 'CMA DMA_CMA ZRAM_MULTI_COMP ZRAM_BACKEND_LZ4 ZRAM_BACKEND_ZSTD ZRAM_TRACK_ENTRY_ACTIME LRU_GEN LRU_GEN_ENABLED FLATMEM SECCOMP SECCOMP_FILTER STACKPROTECTOR_STRONG STRICT_MODULE_RWX'.split()
required_available='ZRAM NF_TABLES NFT_CT NFT_COMPAT NF_NAT NFT_NAT NFT_MASQ NFT_REDIR NETFILTER_XT_MATCH_STATE CFG80211 BT BT_RFCOMM BT_BNEP BT_HIDP'.split()
errors=[]
if config.get("OVPN") != "m":
    errors.append("OVPN must be a loadable module for OpenVPN DCO")
for key in required_builtin:
    if config.get(key)!='y':errors.append(key+' must be built in')
for key in required_available:
    if config.get(key) not in ('y','m'):errors.append(key+' must be available')
for key in ('SPARSEMEM', 'STRICT_KERNEL_RWX', 'ION_CMA_HEAP'):
    if config.get(key) in ('y','m'):errors.append(key+' must remain disabled for the selected memory profile')
for key in ('PCI','ATA','ACPI','ARCH_VIRT','ARCH_SIFIVE','ARCH_SUNXI','SOC_STARFIVE'):
    if config.get(key) in ('y','m'):errors.append(key+' is unrelated to this board configuration')
trimmed = json.loads((Path(__file__).resolve().parents[1] / 'firmware/kernel/trimmed-facilities.json').read_text())
for reason, keys in trimmed.items():
    for key in keys:
        if config.get(key) in ('y', 'm'):
            errors.append(key + ' must remain disabled: ' + reason)
if config.get('LOCALVERSION')!='"-nanokvm-enhanced"':errors.append('unexpected LOCALVERSION')
if errors:raise SystemExit('Invalid Enhanced kernel config:\n'+'\n'.join(errors))
print('NanoKVM kernel configuration gate PASS')

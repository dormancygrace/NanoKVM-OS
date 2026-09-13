# SD layout v2 (beta-9)

Beta-9 requires a complete SD image when coming from beta-8 or earlier. No beta-9 `.nkos` artifact is distributed.

| Partition | Start sector | Sectors | Size |
| --- | ---: | ---: | ---: |
| Boot, FAT | 1 | 131072 | 64 MiB |
| System, ext4 | 131073 | 3047424 | 1488 MiB |
| Data, exFAT | 3180544 | Remaining card space | Variable |

Sectors are 512 bytes. The combined boot/system image size and data start remain unchanged from v1: boot grows by 48 MiB while the system partition shrinks by 48 MiB. A controlled RAM reflash can preserve the existing p3 entry and data filesystem; writing a portable image through a card reader does not automatically preserve an existing p3 entry.

The 64 MiB boot partition has room for the running FIT and a pending RAM installer simultaneously. The full-system updater contract is `sg2002-sd-v2`, and validates the new boot/root geometry before installation. Old-layout packages and images require explicit full-image installation rather than a silent partition migration.

The first-boot S01fs script computes the data start from the end of p2 and its 2048-sector alignment; that boundary remains 3180544.

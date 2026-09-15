# Beta-13 acceptance boundaries

Final image ZIP SHA256:
`6eccca5bb63020a4c063e8932b89f8ea9b923b20f429e8ff7b4e0cae6573da95`.

The complete final boot/rootfs payload was installed on PCIe/UXC. The local
installer retained the existing data partition, configuration and add-ons;
that procedure is not a distributed update package and does not mean normal
SD-card flashing preserves settings.

First boot selected pcie and restarted into its Device Tree without manual
repair. Normal services, live H.265 Direct video, 5 GHz Wi-Fi and USB NCM
worked. Existing user-enabled SSH and Python/pip add-ons remained functional.
The saved USB composition had keyboard, mouse, serial, audio and disk off;
these functions were not enabled for this acceptance run. ATX was not pressed.
OLED appearance and fresh-card data partition creation were not qualified.
Capture logged VPSS allocation warnings while observed video continued;
this was not a sustained soak test or a warning-free capture claim.

Host fixtures cover all profile selection outcomes, ambiguous responses,
bus errors and Alpha's no-beta-pin-access invariant. Cube Full, early Alpha
and Lite still require physical qualification. A working identity OLED is
required for reliable identification of early Alpha hardware.

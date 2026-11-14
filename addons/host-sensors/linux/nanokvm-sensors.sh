#!/bin/sh
# nanokvm-sensors for Linux: reads this computer's sensors (temperatures,
# voltages, fan speeds and the like) from the kernel (/sys/class/hwmon) and
# sends a report every few seconds to the NanoKVM plugged into it, over the
# NanoKVM's USB serial port or its USB network. The NanoKVM shows the last
# report in Settings > Services > Computer sensors. The report format is in
# server/hostsensors of NanoKVM OS.
#
# On the NanoKVM's helper disk this file is NanoKVM-Sensors-Setup-Linux.sh.
# POSIX sh (dash, busybox ash); no other program than the usual tools of a
# Linux system (stty, dd, ip, and curl or wget for the USB network).
VERSION=1.0.0

SYS=${NANOKVM_SENSORS_SYS:-/sys}
PROC=${NANOKVM_SENSORS_PROC:-/proc}
BIN=/usr/local/sbin/nanokvm-sensors
UNIT=/etc/systemd/system/nanokvm-sensors.service
# The report format's bounds.
MAX_SENSORS=1024

usage() {
    cat <<EOF
nanokvm-sensors $VERSION: send this computer's sensors to the NanoKVM plugged into it.

  sh $0             (as root, in a terminal) ask: install, update or remove
  sh $0 install     copy this script to $BIN and start it with the
                    system (a systemd service); the options below become
                    the installed agent's
  sh $0 uninstall   stop and remove it
  sh $0 status      what is installed and whether it runs
  sh $0 run         the agent itself, until stopped
  sh $0 --once      print one report and exit
  sh $0 --version   print the version

Options:
  --serial [TTY]      USB serial only; the port is found by the USB IDs
  --network [URL]     USB network only; URL such as https://10.1.2.1
  --usb-id VID:PID    the NanoKVM's USB IDs when changed (default 3346:1009)
  --interval SECONDS  period, 1 to 3600 (default 5)

Without --serial or --network each report goes to the serial port when the
NanoKVM has one that no login console uses, and to the USB network otherwise.
The NanoKVM takes one of the two: Settings > Services > Computer sensors.
EOF
}

die() {
    echo "nanokvm-sensors: $*" >&2
    exit 1
}

# say: a state change, once.
STATE=
say() {
    [ "$1" = "$STATE" ] && return
    STATE=$1
    echo "$(date '+%Y-%m-%d %H:%M:%S') $1" >&2
}

# ---- the report -----------------------------------------------------------

# json TEXT: a JSON string the NanoKVM accepts: printable ASCII, at most 96
# characters.
json() {
    printf '"%s"' "$(printf '%s' "$1" | tr -cd ' -~' | cut -c1-96 | sed 's/[\\"]/\\&/g; s/^ *//; s/ *$//')"
}

# first FILE: its first line, without a fork; empty when unreadable.
first() {
    _v=
    [ -r "$1" ] && IFS= read -r _v < "$1" 2>/dev/null
    printf '%s' "$_v"
}

# The class of a part by its kernel driver.
class_of() {
    case "$1" in
        coretemp|k10temp|k8temp|zenpower|zenergy|cpu_thermal|via_cputemp|fam15h_power|amd_energy) echo cpu ;;
        nvme|drivetemp|sata*) echo storage ;;
        amdgpu|radeon|nouveau|nvidia*|i915|xe) echo gpu ;;
        acpitz|it8*|nct*|w83*|f718*|asus*|gigabyte*|dell_smm|thinkpad|hp_wmi*|applesmc|sch5*|fintek*|pc87*|smsc*|system76*) echo motherboard ;;
        BAT*|*battery*) echo battery ;;
        *psu*) echo psu ;;
        corsair*|nzxt*|aquacomputer*|gpd_fan|pwm_fan|pwmfan) echo cooler ;;
        r8169*|iwlwifi*|ath*|mt7*|rtw*|igb*|igc*|ixgbe*|mlx*|bnx*|e1000*|enp*|eth*|phy*|tg3|nfp|qed*|sfp*) echo network ;;
        spd5118|jc42|ee1004) echo memory ;;
        *) echo other ;;
    esac
}

# plan: every reading the kernel offers, one line each:
#   file|unit|{"hw":...,"type":...,"name":...,"kind":"..."
# unit says how the file's number becomes the report's: milli (1/1000),
# micro (1/1000000), one (as is), pwm (0..255 to percent). Made at the start
# and again every minute (a drive or a card may come and go); the names take
# a few forks each, the values none.
plan() {
    PLAN=
    _seen=
    CPU_MODEL=$(sed -n 's/^model name[^:]*: *//p' "$PROC/cpuinfo" 2>/dev/null | head -n 1)
    for _h in "$SYS"/class/hwmon/hwmon*; do
        [ -d "$_h" ] || continue
        _driver=$(first "$_h/name")
        [ -n "$_driver" ] || continue
        _hw=$_driver
        # A drive names its model (padded with spaces in sysfs), the
        # processor's sensor the processor.
        for _m in "$_h/device/model" "$_h/device/device/model"; do
            _model=$(first "$_m")
            # shellcheck disable=SC2086 # the splitting drops the padding
            [ -n "$_model" ] && { set -- $_model; _hw="$* ($_driver)"; break; }
        done
        if [ "$(class_of "$_driver")" = cpu ] && [ -n "$CPU_MODEL" ]; then
            _hw="$CPU_MODEL ($_driver)"
        fi
        # Two parts of one name (two drives of a model) are told apart.
        case "|$_seen" in *"|$_hw|"*) _hw="$_hw ${_h##*hwmon}" ;; esac
        _seen="$_seen$_hw|"
        _head="{\"hw\":$(json "$_hw"),\"type\":\"$(class_of "$_driver")\""
        for _f in "$_h"/temp*_input "$_h"/fan*_input "$_h"/in*_input "$_h"/curr*_input \
                  "$_h"/power*_input "$_h"/power*_average "$_h"/humidity*_input "$_h"/pwm[0-9] "$_h"/pwm[0-9][0-9]; do
            [ -r "$_f" ] || continue
            _base=${_f##*/}
            _n=${_base%%_*}
            case "$_base" in
                temp*) _kind=temperature; _unit=milli; _name="Temperature ${_n#temp}" ;;
                fan*) _kind=fan; _unit=one; _name="Fan ${_n#fan}" ;;
                in*) _kind=voltage; _unit=milli; _name="Voltage ${_n#in}" ;;
                curr*) _kind=current; _unit=milli; _name="Current ${_n#curr}" ;;
                power*) _kind=power; _unit=micro; _name="Power ${_n#power}" ;;
                humidity*) _kind=humidity; _unit=milli; _name="Humidity ${_n#humidity}" ;;
                pwm*) _kind=control; _unit=pwm; _name="Fan control ${_base#pwm}"; _n=$_base ;;
            esac
            _label=$(first "$_h/${_n}_label")
            [ -n "$_label" ] && _name=$_label
            PLAN="$PLAN$_f|$_unit|$_head,\"name\":$(json "$_name"),\"kind\":\"$_kind\"
"
        done
    done
}

# number VALUE UNIT: the file's integer as the report's decimal number.
number() {
    _v=$1
    case "$_v" in ''|*[!0-9-]*|-) return 1 ;; esac
    _s=
    [ "$_v" -lt 0 ] && { _s=-; _v=$((0 - _v)); }
    case "$2" in
        milli) printf '%s%d.%03d' "$_s" $((_v / 1000)) $((_v % 1000)) ;;
        micro) printf '%s%d.%03d' "$_s" $((_v / 1000000)) $((_v % 1000000 / 1000)) ;;
        pwm) printf '%d' $((_v * 100 / 255)) ;;
        *) printf '%s%d' "$_s" "$_v" ;;
    esac
}

CPU_TOTAL=
CPU_IDLE=
# The processor's load since the last report, and the memory in use: the
# two readings every Linux has, also where no sensor driver is loaded.
system_readings() {
    _head="{\"hw\":$HOST_JSON,\"type\":\"other\""
    if IFS=' ' read -r _cpu _u _n _sy _i _io _irq _sirq _st _rest < "$PROC/stat" 2>/dev/null && [ "$_cpu" = cpu ]; then
        _total=$((_u + _n + _sy + _i + ${_io:-0} + ${_irq:-0} + ${_sirq:-0} + ${_st:-0}))
        _idle=$((_i + ${_io:-0}))
        if [ -n "$CPU_TOTAL" ] && [ "$_total" -gt "$CPU_TOTAL" ]; then
            _busy=$(( ((_total - CPU_TOTAL) - (_idle - CPU_IDLE)) * 1000 / (_total - CPU_TOTAL) ))
            SENSORS="$SENSORS,$_head,\"name\":\"Processor load\",\"kind\":\"load\",\"value\":$((_busy / 10)).$((_busy % 10))}"
        fi
        CPU_TOTAL=$_total
        CPU_IDLE=$_idle
    fi
    _mt=
    _ma=
    while IFS=': ' read -r _key _val _unit; do
        case "$_key" in
            MemTotal) _mt=$_val ;;
            MemAvailable) _ma=$_val; break ;;
        esac
    done < "$PROC/meminfo" 2>/dev/null
    if [ -n "$_mt" ] && [ -n "$_ma" ] && [ "$_mt" -gt 0 ]; then
        _used=$((_mt - _ma))
        SENSORS="$SENSORS,$_head,\"name\":\"Memory used\",\"kind\":\"load\",\"value\":$((_used * 100 / _mt))}"
        SENSORS="$SENSORS,$_head,\"name\":\"Memory used\",\"kind\":\"data\",\"value\":$(number $((_used * 1000 / 1048576)) milli)}"
        SENSORS="$SENSORS,$_head,\"name\":\"Memory available\",\"kind\":\"data\",\"value\":$(number $((_ma * 1000 / 1048576)) milli)}"
    fi
}

report() {
    SENSORS=
    _count=0
    _old_ifs=$IFS
    IFS='
'
    for _line in $PLAN; do
        [ "$_count" -lt "$MAX_SENSORS" ] || break
        _file=${_line%%|*}
        _tail=${_line#*|}
        _unit=${_tail%%|*}
        _json=${_tail#*|}
        _raw=
        IFS= read -r _raw < "$_file" 2>/dev/null || continue
        _value=$(number "$_raw" "$_unit") || continue
        SENSORS="$SENSORS,$_json,\"value\":$_value}"
        _count=$((_count + 1))
    done
    IFS=$_old_ifs
    system_readings
    printf '{"v":1,"host":%s,"os":%s,"agent":"nanokvm-sensors/%s linux","interval":%d,"sensors":[%s]}' \
        "$HOST_JSON" "$OS_JSON" "$VERSION" "$INTERVAL" "${SENSORS#,}"
}

# ---- the transports -------------------------------------------------------

# limit SECONDS COMMAND...: end a command that does not finish (a write to a
# port nobody reads). timeout(1) when there is one, a watchdog otherwise.
limit() {
    _t=$1
    shift
    if command -v timeout > /dev/null 2>&1; then
        timeout "$_t" "$@"
        return
    fi
    "$@" &
    _pid=$!
    (sleep "$_t"; kill "$_pid" 2>/dev/null) &
    _dog=$!
    wait "$_pid"
    _r=$?
    kill "$_dog" 2>/dev/null
    return $_r
}

# find_tty: the NanoKVM's serial port (a ttyACM of the USB device with its
# IDs), in TTY.
find_tty() {
    TTY=
    [ -n "$PORT" ] && { TTY=$PORT; return 0; }
    for _t in "$SYS"/class/tty/ttyACM*; do
        [ -e "$_t/device" ] || continue
        # The interface's parent is the USB device.
        _d=$(cd -P "$_t/device" 2>/dev/null && cd .. && pwd -P) || continue
        [ "$(first "$_d/idVendor"):$(first "$_d/idProduct")" = "$USB_ID" ] || continue
        TTY=/dev/${_t##*/}
        return 0
    done
    return 1
}

# console_on TTY: a login console has the port (the serial console helper
# set one up): the reports would be typed into its prompt.
console_on() {
    _name=${1##*/}
    if command -v systemctl > /dev/null 2>&1 && systemctl is-active --quiet "serial-getty@$_name.service" 2>/dev/null; then
        return 0
    fi
    grep -qs "^[^#].*$_name" /etc/inittab
}

# send_serial REPORT. Nothing is written to a port nobody reads: the NanoKVM
# says "nanokvm-ready" on the port every second while it reads reports
# (Computer sensors set to USB serial), and a report is written only after
# hearing that. What had arrived before is dropped first, so an old "ready"
# does not count.
send_serial() {
    find_tty || { REASON="no NanoKVM serial port (USB serial is not in its USB composition)"; return 1; }
    [ -c "$TTY" ] || { REASON="$TTY is not a serial port"; return 1; }
    if console_on "$TTY"; then
        REASON="$TTY is this computer's login console"
        return 1
    fi
    # Raw, without waiting for a carrier and without dropping the lines at
    # every close; reads return at once (min 0 time 0).
    limit 5 stty -F "$TTY" raw -echo clocal -hupcl min 0 time 0 2>/dev/null ||
        { REASON="$TTY cannot be set up (not root, or output is stuck on it)"; return 1; }
    dd if="$TTY" bs=4096 count=16 > /dev/null 2>&1
    # Now wait up to 2.5 s for the next "ready".
    stty -F "$TTY" min 0 time 25 2>/dev/null
    case "$(dd if="$TTY" bs=64 count=1 2>/dev/null | tr -cd 'a-z-')" in
        *nanokvm-ready*) ;;
        *)
            REASON="$TTY: the NanoKVM is not reading the port (Computer sensors is not set to USB serial)"
            return 1
            ;;
    esac
    # An empty line first: what a write that was cut left on the wire ends
    # there, and the NanoKVM skips empty lines.
    if printf '\n%s\n' "$1" | limit 5 dd of="$TTY" bs=65536 2>/dev/null; then
        say "sending to $TTY"
        return 0
    fi
    REASON="$TTY: the NanoKVM did not take the report in time"
    return 1
}

# find_url: the NanoKVM on its USB network, in URL. The adapter is known by
# the first four bytes of its address (the NanoKVM gives the computer's side
# 48:da:35:6d). With an IPv4 address on it (DHCP), the NanoKVM is .1 of that
# network. Without one, which is usual on a server that configures no new
# adapter by itself, the agent gives the adapter an address itself: the
# NanoKVM's network is 10.X.Y.0/24 with X and Y the adapter address's last
# two bytes (both come from the same bytes of the chip's id), the NanoKVM is
# .1, its DHCP hands out .100 to .200, and the agent takes .2. That is an
# address on the NanoKVM's own link and nothing else: no route, no DNS, and
# gone with the adapter. A NanoKVM whose USB network prefix was changed
# needs DHCP on the adapter, or --network URL.
find_url() {
    [ -n "$URL_OPTION" ] && { URL=$URL_OPTION; return 0; }
    for _n in "$SYS"/class/net/*; do
        [ -d "$_n" ] || continue
        _mac=$(first "$_n/address")
        case "$_mac" in 48:da:35:6d:*) ;; *) continue ;; esac
        _if=${_n##*/}
        _ip=$(ip -4 -o addr show dev "$_if" 2>/dev/null | sed -n 's/.* inet \([0-9]*\.[0-9]*\.[0-9]*\)\.[0-9]*\/.*/\1/p' | head -n 1)
        if [ -z "$_ip" ]; then
            _a=${_mac#48:da:35:6d:}
            _x=$(printf '%d' "0x${_a%:*}")
            _y=$(printf '%d' "0x${_a#*:}")
            # As S30usbnet does.
            [ "$_x" -ne "$_y" ] || _x=$((_x + 1))
            [ "$_x" -lt 255 ] || _x=253
            [ "$_y" -lt 255 ] || _y=254
            _ip=10.$_x.$_y
            # Not when the computer already has that network elsewhere.
            if ip -4 route show "$_ip.0/24" 2>/dev/null | grep -q .; then
                REASON="the NanoKVM's network $_ip.0/24 is in use on this computer: configure DHCP on $_if"
                return 1
            fi
            ip addr add "$_ip.2/24" dev "$_if" 2>/dev/null && ip link set dev "$_if" up 2>/dev/null ||
                { REASON="$_if has no address and cannot be given one (not root?)"; return 1; }
        fi
        URL="https://$_ip.1"
        return 0
    done
    REASON="no NanoKVM USB network (it is not in its USB composition)"
    return 1
}

send_network() {
    find_url || return 1
    # The NanoKVM's certificate is its own; on the USB link the peer is the
    # device at the other end of the cable and the report holds nothing
    # secret, so for an address found on that adapter it is not checked. A
    # URL given with --network is checked as usual.
    _insecure=-k
    _nocheck=--no-check-certificate
    [ -n "$URL_OPTION" ] && { _insecure=; _nocheck=; }
    if command -v curl > /dev/null 2>&1; then
        _code=$(printf '%s' "$1" | curl -sS $_insecure -m 5 -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
            --data-binary @- "$URL/api/host-sensors" 2>/dev/null)
    elif command -v wget > /dev/null 2>&1; then
        _tmp=$(mktemp) || { REASON="no temporary file"; return 1; }
        printf '%s' "$1" > "$_tmp"
        if wget -q -T 5 $_nocheck --header='Content-Type: application/json' --post-file="$_tmp" -O /dev/null "$URL/api/host-sensors" 2>/dev/null; then
            _code=204
        else
            _code=000
        fi
        rm -f "$_tmp"
    else
        REASON="neither curl nor wget for the USB network"
        return 1
    fi
    case "$_code" in
        2*) say "sending to $URL"; return 0 ;;
        403) REASON="$URL: refused (Computer sensors is not set to USB network)" ;;
        000) REASON="$URL: no answer" ;;
        *) REASON="$URL: HTTP $_code" ;;
    esac
    return 1
}

send() {
    case "$TRANSPORT" in
        serial) send_serial "$1" || say "$REASON" ;;
        network) send_network "$1" || say "$REASON" ;;
        *)
            send_serial "$1" && return
            _first=$REASON
            send_network "$1" || say "$_first; $REASON"
            ;;
    esac
}

run() {
    HOST_JSON=$(json "$(hostname 2>/dev/null || first "$PROC/sys/kernel/hostname")")
    _os=$(. /etc/os-release 2>/dev/null && printf '%s' "$PRETTY_NAME")
    OS_JSON=$(json "${_os:-Linux} $(first "$PROC/sys/kernel/osrelease")")
    plan
    if [ -n "$ONCE" ]; then
        # The load needs two looks at the processor's counters.
        system_readings > /dev/null
        sleep 1
        report
        echo
        return
    fi
    say "started, version $VERSION"
    trap 'exit 0' INT TERM
    _cycle=0
    while :; do
        send "$(report)"
        sleep "$INTERVAL" &
        wait $!
        _cycle=$((_cycle + 1))
        [ $((_cycle * INTERVAL % 60)) -lt "$INTERVAL" ] && plan
    done
}

# ---- start with the system ------------------------------------------------

need_root() {
    [ "$(id -u)" = 0 ] || die "$1 needs root"
}

installed_version() {
    [ -f "$BIN" ] && sed -n 's/^VERSION=//p' "$BIN" | head -n 1
}

# newer A B: version A is newer than B (numbers separated by dots).
newer() {
    [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1)" = "$1" ]
}

install_agent() {
    need_root install
    command -v systemctl > /dev/null 2>&1 && [ -d /run/systemd/system ] ||
        die "no systemd here: copy this script somewhere and start \"sh nanokvm-sensors run\" from your init system"
    # The copy runs from the disk of this computer: the helper disk is
    # read-only and goes away with the NanoKVM's USB composition.
    mkdir -p "${BIN%/*}"
    cp "$0" "$BIN.new" && chmod 0755 "$BIN.new" && mv "$BIN.new" "$BIN" || die "cannot write $BIN"
    cat > "$UNIT" <<EOF
[Unit]
Description=NanoKVM computer sensors agent (sends this computer's sensors to the NanoKVM plugged into it)
Documentation=https://github.com/dormancygrace/NanoKVM-OS/blob/main/docs/HOST-SENSORS.md

[Service]
ExecStart=/bin/sh $BIN run $AGENT_OPTIONS
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable --now nanokvm-sensors.service > /dev/null 2>&1 || die "the service did not start: systemctl status nanokvm-sensors"
    systemctl restart nanokvm-sensors.service
    echo "nanokvm-sensors $VERSION is installed and running (systemctl status nanokvm-sensors)."
    echo "Its readings: NanoKVM > Settings > Services > Computer sensors."
}

uninstall_agent() {
    need_root uninstall
    if command -v systemctl > /dev/null 2>&1; then
        systemctl disable --now nanokvm-sensors.service > /dev/null 2>&1
    fi
    rm -f "$UNIT" "$BIN"
    command -v systemctl > /dev/null 2>&1 && systemctl daemon-reload
    echo "nanokvm-sensors is removed."
}

status() {
    _have=$(installed_version)
    echo "This script: nanokvm-sensors $VERSION"
    echo "Installed: ${_have:-none}"
    if command -v systemctl > /dev/null 2>&1; then
        echo "Service: $(systemctl is-active nanokvm-sensors.service 2>/dev/null)"
    fi
    if find_tty; then
        if console_on "$TTY"; then _note=" (this computer's login console: not used for sensors)"; else _note=; fi
        echo "NanoKVM serial port: $TTY$_note"
    else
        echo "NanoKVM serial port: none"
    fi
    # Only a look: status does not configure the adapter.
    _net=none
    for _n in "$SYS"/class/net/*; do
        [ -d "$_n" ] || continue
        case "$(first "$_n/address")" in 48:da:35:6d:*) _net="adapter ${_n##*/}" ;; esac
    done
    echo "NanoKVM USB network: $_net"
}

# setup: run without arguments in a terminal: what the Windows agent's
# window does on a double click.
setup() {
    status
    _have=$(installed_version)
    echo
    if [ -z "$_have" ]; then
        printf 'Install nanokvm-sensors %s? [Y/n] ' "$VERSION"
        read -r _answer || exit 0
        case "$_answer" in ''|y|Y|yes) install_agent ;; esac
    elif newer "$VERSION" "$_have"; then
        printf '[U]pdate to %s, [r]emove, or [q]uit? ' "$VERSION"
        read -r _answer || exit 0
        case "$_answer" in ''|u|U) install_agent ;; r|R) uninstall_agent ;; esac
    else
        printf '[R]emove, or [q]uit? '
        read -r _answer || exit 0
        case "$_answer" in r|R) uninstall_agent ;; esac
    fi
}

# ---- the command line -----------------------------------------------------

COMMAND=
TRANSPORT=auto
PORT=
URL_OPTION=
USB_ID=3346:1009
INTERVAL=5
ONCE=
AGENT_OPTIONS=
[ $# -eq 0 ] && COMMAND=setup
while [ $# -gt 0 ]; do
    case "$1" in
        install|uninstall|run|status|setup) COMMAND=$1 ;;
        --once) ONCE=1; COMMAND=run ;;
        --version) echo "$VERSION"; exit 0 ;;
        -h|--help) usage; exit 0 ;;
        --serial)
            TRANSPORT=serial
            AGENT_OPTIONS="$AGENT_OPTIONS --serial"
            case "${2:-}" in /dev/*) PORT=$2; AGENT_OPTIONS="$AGENT_OPTIONS $2"; shift ;; esac
            ;;
        --network)
            TRANSPORT=network
            AGENT_OPTIONS="$AGENT_OPTIONS --network"
            case "${2:-}" in http://*|https://*) URL_OPTION=${2%/}; AGENT_OPTIONS="$AGENT_OPTIONS $2"; shift ;; esac
            ;;
        --usb-id)
            case "${2:-}" in [0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]:[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]) ;; *) die "--usb-id needs VID:PID, four hex digits each" ;; esac
            USB_ID=$(printf '%s' "$2" | tr 'A-F' 'a-f')
            AGENT_OPTIONS="$AGENT_OPTIONS --usb-id $USB_ID"
            shift
            ;;
        --interval)
            case "${2:-}" in ''|*[!0-9]*) die "--interval needs 1 to 3600 seconds" ;; esac
            [ "$2" -ge 1 ] && [ "$2" -le 3600 ] || die "--interval needs 1 to 3600 seconds"
            INTERVAL=$2
            AGENT_OPTIONS="$AGENT_OPTIONS --interval $2"
            shift
            ;;
        *) die "unknown argument $1 (see --help)" ;;
    esac
    shift
done

case "${COMMAND:-run}" in
    setup)
        [ -t 0 ] || { usage; exit 0; }
        need_root "setting the agent up"
        setup
        ;;
    install) install_agent ;;
    uninstall) uninstall_agent ;;
    status) status ;;
    run) run ;;
esac

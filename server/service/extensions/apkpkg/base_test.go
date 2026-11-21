package apkpkg

import (
	"strings"
	"testing"
)

// The retry after a failed transaction: a package left broken is reinstalled,
// one installed only as a dependency is selected, a broken dependency is
// reinstalled without being selected, a world entry with a tag or a constraint
// is kept, and an excluded package stays out.
func TestBasePlanRepairsAndSelects(t *testing.T) {
	world := []byte("lldpd\nlldpd-openrc\nnet-snmp-openrc\nnut@community\nnut-openrc<3\n!openvpn\n")
	missing := []string{"nut-openrc", "openvpn"}
	broken := []string{"lldpd", "net-snmp", "net-snmp-libs"}
	fix, add := basePlan(missing, broken, world)
	if got := strings.Join(fix, ","); got != "lldpd,net-snmp,nut-openrc,net-snmp-libs" {
		t.Fatalf("fix: %s", got)
	}
	if got := strings.Join(add, ","); got != "net-snmp" {
		t.Fatalf("add: %s", got)
	}
	if fix, add = basePlan(nil, nil, []byte(strings.Join(basePackages, "\n"))); len(fix)+len(add) != 0 {
		t.Fatalf("complete set: fix %v, add %v", fix, add)
	}
}

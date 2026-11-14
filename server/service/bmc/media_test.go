package bmc

import (
	"context"
	"errors"
	"testing"

	"NanoKVM-Server/internal/bmc/redfish"
	"NanoKVM-Server/service/storage"
)

type fakeDrive struct {
	medium storage.Medium
	calls  []string
	err    error
}

func (f *fakeDrive) Medium() (storage.Medium, error) { return f.medium, nil }
func (f *fakeDrive) EjectMedium() error              { f.calls = append(f.calls, "eject"); return f.err }
func (f *fakeDrive) InsertURL(_ context.Context, url, user, _ string) error {
	f.calls = append(f.calls, "url "+url+" "+user)
	return f.err
}
func (f *fakeDrive) MountImageFile(name string, cdrom bool) error {
	f.calls = append(f.calls, "file "+name+map[bool]string{true: " cd", false: " disk"}[cdrom])
	return f.err
}

func TestRedfishMediaChoosesTheDriveByTheImage(t *testing.T) {
	yes, no := true, false
	drive := &fakeDrive{}
	media := RedfishMedia(drive)
	for _, c := range []struct {
		image     string
		protected *bool
		call      string // "" when refused before the drive
	}{
		{"https://h/a.iso?sig=1", nil, "url https://h/a.iso?sig=1 u"},
		{"smb://h/s/a.iso", &yes, "url smb://h/s/a.iso u"},
		{"http://h/a.iso", &no, ""},
		{"file:///data/a.iso", nil, "file /data/a.iso cd"},
		{"file:///data/a.iso", &no, ""},
		{"file:///data/d.img", &no, "file /data/d.img disk"},
		{"file:///data/d.img", &yes, ""},
		{"file://host/data/a.iso", nil, ""},
		{"nfs://h/a.iso", nil, ""},
	} {
		drive.calls = nil
		err := media.Insert(context.Background(), redfish.MediaRequest{Image: c.image, UserName: "u", WriteProtected: c.protected})
		if got := append(drive.calls, "")[0]; got != c.call || (err == nil) != (c.call != "") {
			t.Errorf("%s protected=%v: drive call %q, err %v; want %q", c.image, c.protected, got, err, c.call)
		}
	}
	// A drive held by something else is told apart from a bad request.
	drive.err = storage.ErrNetbootMedium
	if err := media.Eject(context.Background()); !errors.Is(err, redfish.ErrMediaInUse) {
		t.Errorf("eject under network boot: %v", err)
	}
	drive.err, drive.medium = nil, storage.Medium{Inserted: true, Source: "local", Image: "/data/a.iso", CDROM: true}
	if m, _ := media.Medium(context.Background()); m.Image != "file:///data/a.iso" || m.Via != "URI" || !m.CD {
		t.Errorf("a local image: %+v", m)
	}
}

package bmc

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"NanoKVM-Server/internal/bmc/redfish"
	"NanoKVM-Server/service/storage"
)

// Drive is what Redfish's virtual media needs from the storage service: the
// one virtual drive the web page, MCP and network boot share.
type Drive interface {
	Medium() (storage.Medium, error)
	InsertURL(ctx context.Context, rawURL, username, password string) error
	MountImageFile(name string, cdrom bool) error
	EjectMedium() error
}

// redfishMedia is the drive as Redfish's VirtualMedia.
//
//   - http://, https://: an ISO read from a web server as the computer asks
//     for it (the page's "From URL"); smb:// the same from a file share, with
//     UserName and Password. A CD/DVD drive, read-only.
//   - file:///data/NAME: an image on the NanoKVM's card, one the page's
//     image list shows: .iso as a CD/DVD drive, .img as a writable disk.
type redfishMedia struct{ drive Drive }

// RedfishMedia is drive as Redfish's virtual media.
func RedfishMedia(drive Drive) redfish.Media { return redfishMedia{drive} }

func (m redfishMedia) Medium(context.Context) (redfish.Medium, error) {
	current, err := m.drive.Medium()
	if err != nil {
		return redfish.Medium{}, err
	}
	medium := redfish.Medium{Inserted: current.Inserted, Image: current.Image, CD: current.CDROM, Via: "URI"}
	switch current.Source {
	case "local":
		medium.Image = "file://" + current.Image
	case "browser":
		medium.Via = "Applet"
	case "netboot":
		medium.Via, medium.Image = "Oem", "file://"+current.Image
	}
	return medium, nil
}

func (m redfishMedia) Insert(ctx context.Context, request redfish.MediaRequest) error {
	u, err := url.Parse(request.Image)
	if err != nil {
		return errors.New("Image is not a URL.")
	}
	writable := request.WriteProtected != nil && !*request.WriteProtected
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "smb":
		if writable {
			return errors.New("An image from a URL is a read-only CD/DVD: WriteProtected must be true.")
		}
		err = m.drive.InsertURL(ctx, request.Image, request.UserName, request.Password)
	case "file":
		if u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("A local image is file:///data/NAME.iso or file:///data/NAME.img.")
		}
		cdrom := strings.HasSuffix(strings.ToLower(u.Path), ".iso")
		if cdrom && writable {
			return errors.New("An .iso image is a read-only CD/DVD: WriteProtected must be true.")
		}
		if !cdrom && request.WriteProtected != nil && *request.WriteProtected {
			return errors.New("An .img image is a writable disk: WriteProtected must be false.")
		}
		err = m.drive.MountImageFile(u.Path, cdrom)
	default:
		return errors.New("Image must be an http://, https:// or smb:// URL, or file:///data/NAME for an image on the NanoKVM.")
	}
	return inUse(err)
}

func (m redfishMedia) Eject(context.Context) error {
	return inUse(m.drive.EjectMedium())
}

// inUse tells the refusals that mean "something else holds the drive" from
// the others, for the client's status code.
func inUse(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, storage.ErrNetbootMedium) || errors.Is(err, storage.ErrDriveInUse) {
		return errors.Join(redfish.ErrMediaInUse, err)
	}
	return err
}

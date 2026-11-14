package redfish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"
)

// The drive is the system's since VirtualMedia 1.4 (sushy looks there
// first); under the manager, where it was before and where older clients
// look (Ansible's redfish_command by default), the same drive is served
// too. Each place links to its own copy.
const (
	mediaSuffix  = "/VirtualMedia"
	insertSuffix = "/1/Actions/VirtualMedia.InsertMedia"
	ejectSuffix  = "/1/Actions/VirtualMedia.EjectMedia"

	// An InsertMedia answers when the computer has the medium, or with the
	// reason it did not connect; a source that does not answer ends here.
	insertTimeout = 45 * time.Second
)

// Medium is what is in the virtual drive.
type Medium struct {
	Inserted bool
	// Image is where it comes from, never with credentials or a query.
	Image string
	// CD: shown to the computer as a CD/DVD drive (read-only); a disk
	// otherwise (writable).
	CD bool
	// Via is the ConnectedVia value: "URI", "Applet" (a browser's file) or
	// "Oem" (network boot).
	Via string
}

// MediaRequest is an InsertMedia.
type MediaRequest struct {
	Image, UserName, Password string
	// WriteProtected as asked for; nil when the request does not say.
	WriteProtected *bool
}

// Media is the virtual drive. Insert returns when the computer has the
// medium. Its errors are shown to the client: ErrMediaInUse when something
// else holds the drive, any other as the reason the request is refused.
type Media interface {
	Medium(ctx context.Context) (Medium, error)
	Insert(ctx context.Context, request MediaRequest) error
	Eject(ctx context.Context) error
}

// ErrMediaInUse: the drive holds a medium, or another medium is connecting.
var ErrMediaInUse = errors.New("the virtual drive is in use: eject its medium first")

// mediaBase reports whether path is in a VirtualMedia collection, and under
// which resource.
func mediaBase(path string) (string, bool) {
	for _, parent := range []string{systemPath, managerPath} {
		base := parent + mediaSuffix
		if path == base || strings.HasPrefix(path, base+"/") {
			return base, true
		}
	}
	return "", false
}

func mediaIn(path string) bool {
	_, ok := mediaBase(path)
	return ok
}

func (h *Handler) media(w http.ResponseWriter, r *http.Request, base, at string) {
	switch at {
	case base:
		h.get(w, r, func() any {
			return collection(base, "#VirtualMediaCollection.VirtualMediaCollection", "Virtual Media Collection", base+"/1")
		})
	case base + "/1":
		h.get(w, r, func() any { return h.medium(r.Context(), base) })
	case base + insertSuffix:
		h.insertMedia(w, r)
	case base + ejectSuffix:
		h.mediaAction(w, r, func(ctx context.Context, _ *http.Request) error { return h.Media.Eject(ctx) })
	default:
		writeError(w, http.StatusNotFound, "ResourceMissingAtURI", "The resource at the URI "+r.URL.Path+" was not found.")
	}
}

func (h *Handler) medium(ctx context.Context, base string) any {
	medium := object{
		"@odata.id":   base + "/1",
		"@odata.type": "#VirtualMedia.v1_6_0.VirtualMedia",
		"Id":          "1",
		"Name":        "Virtual drive",
		"Description": "The NanoKVM's virtual USB drive: a CD/DVD drive for an ISO image, a disk for a disk image",
		"MediaTypes":  []string{"CD", "DVD", "USBStick"},
		// The image is read as the computer asks for it, never copied.
		"TransferMethod": "Stream",
		"Image":          nil,
		"ImageName":      nil,
		"Inserted":       false,
		"WriteProtected": true,
		"ConnectedVia":   "NotConnected",
		"Actions": object{
			"#VirtualMedia.InsertMedia": object{
				"target": base + insertSuffix,
				"TransferProtocolType@Redfish.AllowableValues": []string{"HTTP", "HTTPS", "CIFS"},
				"TransferMethod@Redfish.AllowableValues":       []string{"Stream"},
			},
			"#VirtualMedia.EjectMedia": object{"target": base + ejectSuffix},
		},
	}
	current, err := h.Media.Medium(ctx)
	if err != nil {
		medium["Status"] = object{"State": "UnavailableOffline"}
		return medium
	}
	medium["Status"] = object{"State": "Enabled", "Health": "OK"}
	if !current.Inserted {
		return medium
	}
	medium["Image"] = current.Image
	medium["ImageName"] = path.Base(current.Image)
	medium["Inserted"] = true
	medium["WriteProtected"] = current.CD
	medium["ConnectedVia"] = current.Via
	switch scheme, _, _ := strings.Cut(current.Image, "://"); strings.ToLower(scheme) {
	case "http":
		medium["TransferProtocolType"] = "HTTP"
	case "https":
		medium["TransferProtocolType"] = "HTTPS"
	case "smb":
		medium["TransferProtocolType"] = "CIFS"
	}
	return medium
}

// mediaAction runs an action that changes the medium and answers for it.
// The change is carried out even if the client disconnects.
func (h *Handler) mediaAction(w http.ResponseWriter, r *http.Request, action func(context.Context, *http.Request) error) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "OperationNotAllowed", "Use POST for actions.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), insertTimeout)
	defer cancel()
	switch err := action(ctx, r); {
	case errors.Is(err, ErrMediaInUse):
		writeError(w, http.StatusConflict, "ResourceInUse", err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, "ActionParameterValueError", err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) insertMedia(w http.ResponseWriter, r *http.Request) {
	h.mediaAction(w, r, func(ctx context.Context, r *http.Request) error {
		var body struct {
			Image, UserName, Password            string
			TransferProtocolType, TransferMethod string
			Inserted, WriteProtected             *bool
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
			return errors.New("The request body is not valid JSON.")
		}
		switch {
		case body.Image == "":
			return errors.New("The Image parameter is required.")
		case body.Inserted != nil && !*body.Inserted:
			return errors.New("Inserted must be true: an image is put into the drive when it is given.")
		case body.TransferMethod != "" && body.TransferMethod != "Stream":
			return errors.New("TransferMethod must be Stream: the image is not copied to the NanoKVM.")
		}
		if body.TransferProtocolType != "" {
			scheme, _, _ := strings.Cut(body.Image, "://")
			want := map[string]string{"HTTP": "http", "HTTPS": "https", "CIFS": "smb"}[body.TransferProtocolType]
			if want == "" || !strings.EqualFold(scheme, want) {
				return errors.New("TransferProtocolType must be HTTP, HTTPS or CIFS and match the image's address (http://, https://, smb://).")
			}
		}
		current, err := h.Media.Medium(ctx)
		if err != nil {
			return err
		}
		if current.Inserted {
			return ErrMediaInUse
		}
		return h.Media.Insert(ctx, MediaRequest{Image: body.Image, UserName: body.UserName, Password: body.Password, WriteProtected: body.WriteProtected})
	})
}

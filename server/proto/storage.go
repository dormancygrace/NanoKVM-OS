package proto

type GetImagesRsp struct {
	Files []string `json:"files"`
}

type MountImageReq struct {
	File  string `json:"file" validate:"omitempty"`
	Cdrom bool   `json:"cdrom" validate:"omitempty"`
	Force bool   `json:"force" validate:"omitempty"`
}

type GetMountedImageRsp struct {
	File string `json:"file"`
}

type GetCdRomRsp struct {
	Cdrom int64 `json:"cdrom"`
}

type DeleteImageReq struct {
	File string `json:"file" validate:"required"`
}

// UserDiskRsp describes the writable FAT32 image the computer may see as a
// third drive: whether it exists, is enabled in the composition and is
// attached to the gadget now.
type UserDiskRsp struct {
	File       string `json:"file"`
	Label      string `json:"label"`
	Sizes      []int  `json:"sizes"`
	Exists     bool   `json:"exists"`
	SizeMiB    int    `json:"sizeMiB,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	Enabled    bool   `json:"enabled"`
	Attached   bool   `json:"attached"`
}

type CreateUserDiskReq struct {
	SizeMiB int `json:"sizeMiB" validate:"required"`
}

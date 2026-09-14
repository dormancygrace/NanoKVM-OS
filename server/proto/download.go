package proto

type DownloadImageReq struct {
	File      string `json:"file" validate:"required"`
	SHA256Sum string `json:"sha256sum"`
}

type ImageEnabledRsp struct {
	Enabled bool `json:"enabled"`
}

type StatusImageRsp struct {
	DownloadedBytes int64   `json:"downloadedBytes"`
	TotalBytes      int64   `json:"totalBytes"`
	BytesPerSecond  float64 `json:"bytesPerSecond"`
	Status          string  `json:"status"`
	File            string  `json:"file"`
	Percentage      string  `json:"percentage"`
}

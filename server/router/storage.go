package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/storage"
)

func storageRouter(r *gin.Engine) {
	service := storage.NewService()
	api := r.Group("/api").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))

	api.GET("/storage/remote", service.RemoteStatus)
	api.GET("/storage/remote/connect", service.ConnectRemote)
	api.POST("/storage/remote/disconnect", service.DisconnectRemote)
	api.GET("/storage/image", service.GetImages)               // get image list
	api.GET("/storage/image/mounted", service.GetMountedImage) // get mounted image
	api.POST("/storage/image/mount", service.MountImage)       // mount image
	api.GET("/storage/cdrom", service.GetCdRom)                // get CD-ROM flag
	api.POST("/storage/image/delete", service.DeleteImage)     // delete image
	api.GET("/storage/user-disk", service.GetUserDisk)         // the writable FAT32 user disk
	api.POST("/storage/user-disk/create", service.CreateUserDisk)
	api.POST("/storage/user-disk/delete", service.DeleteUserDisk)
	api.GET("/storage/user-disk/files", service.ListUserDiskFiles)
	api.GET("/storage/user-disk/files/download", service.DownloadUserDiskFile)
	api.POST("/storage/user-disk/files/upload", service.UploadUserDiskFile)
	api.POST("/storage/user-disk/files/mkdir", service.MakeUserDiskDirectory)
	api.POST("/storage/user-disk/files/delete", service.DeleteUserDiskFile)
}

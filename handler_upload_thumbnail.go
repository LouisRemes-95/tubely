package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to parse multipart form", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	if len(contentType) == 0 {
		respondWithError(w, http.StatusBadRequest, "Content type not specified", err)
		return
	}

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get meatdata for video id", err)
		return
	}
	if videoMetaData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not the owner of the video", fmt.Errorf(""))
		return
	}

	mediaType, _, err := mime.ParseMediaType(contentType)

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Content type not and image", err)
		return
	}

	exts, _ := mime.ExtensionsByType(mediaType)

	randomByteSlice := make([]byte, 32)
	rand.Read(randomByteSlice)
	randomString := base64.RawURLEncoding.EncodeToString(randomByteSlice)

	assetFilePath := filepath.Join(cfg.assetsRoot, randomString+exts[0])
	assetFile, err := os.Create(assetFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create asset file", err)
		return
	}
	_, err = io.Copy(assetFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to copy to asset file", err)
		return
	}

	dataUrl := "http://localhost:" + cfg.port + "/" + assetFilePath

	if videoMetaData.ThumbnailURL != nil {
		*videoMetaData.ThumbnailURL = dataUrl
	} else {
		videoMetaData.ThumbnailURL = &dataUrl
	}

	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetaData)
}

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

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

	fmt.Println("uploading video file for video", videoID, "by user", userID)

	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get meatdata for video id", err)
		return
	}
	if videoMetaData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not the owner of the video", fmt.Errorf(""))
		return
	}

	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to parse multipart form", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get video key form file", err)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	if len(contentType) == 0 {
		respondWithError(w, http.StatusBadRequest, "Content type not specified", err)
		return
	}

	mediaType, _, err := mime.ParseMediaType(contentType)

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Content type not an mp4", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create a temporary file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save video", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to reset the temp file pointer", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to get aspect ratio of video", err)
		return
	}

	exts, _ := mime.ExtensionsByType(mediaType)
	randomByteSlice := make([]byte, 32)
	rand.Read(randomByteSlice)
	key := aspectRatio + "/" + base64.RawURLEncoding.EncodeToString(randomByteSlice) + exts[0]
	putObjetInput := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        tempFile,
		ContentType: &mediaType,
	}
	_, err = cfg.s3Client.PutObject(r.Context(), &putObjetInput)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to put object in s3 bucket", err)
		return
	}

	dataUrl := "http://" + cfg.s3Bucket + ".s3." + cfg.s3Region + ".amazonaws.com/" + key

	if videoMetaData.VideoURL != nil {
		*videoMetaData.VideoURL = dataUrl
	} else {
		videoMetaData.VideoURL = &dataUrl
	}

	err = cfg.db.UpdateVideo(videoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMetaData)
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run command: %w", err)
	}

	var stdOut struct {
		Streams []struct {
			Width  *int `json:"width"`
			Height *int `json:"height"`
		} `json:"streams"`
	}

	err = json.Unmarshal(buf.Bytes(), &stdOut)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal buffer: %w", err)
	}

	if stdOut.Streams[0].Width == nil || stdOut.Streams[0].Height == nil {
		return "", fmt.Errorf("width and Height not given: %w", err)
	}

	const tol = 1e-2
	switch {
	case math.Abs(float64(*stdOut.Streams[0].Width)/float64(*stdOut.Streams[0].Height)-16.0/9.0) < tol:
		return "landscape", nil
	case math.Abs(float64(*stdOut.Streams[0].Width)/float64(*stdOut.Streams[0].Height)-9.0/16.0) < tol:
		return "portrait", nil
	default:
		return "other", nil
	}
}

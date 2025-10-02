package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to fetch video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the video owner", nil)
		return
	}

	const maxMemory = 10 << 20

	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Cannot parse data", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Cannot process media type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported media type. Supported: 'image/jpeg', 'image/png'", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create temporary file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to write to temporary file", err)
		return
	}

	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {

		respondWithError(w, http.StatusInternalServerError, "Unable to seek to start of file", err)
		return
	}

	s3Key := filepath.Base(tempFile.Name())

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {

		respondWithError(w, http.StatusInternalServerError, "Unable to determine aspect ratio", err)
		return
	}

	switch aspectRatio {
	case "16:9":
		s3Key = "landscape/" + s3Key
	case "9:16":
		s3Key = "portrait/" + s3Key
	default:
		s3Key = "other/" + s3Key
	}

	if _, err := cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         ToPtr(s3Key),
		Body:        tempFile,
		ContentType: ToPtr(mediaType),
	}); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to write to S3", err)
		return
	}

	video.VideoURL = ToPtr(fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s",
		cfg.s3Bucket, cfg.s3Region, s3Key))

	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to write to database", err)
		return
	}
}

func ToPtr[T any](v T) *T {
	return &v
}

func getVideoAspectRatio(filePath string) (string, error) {
	type output struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	buffer := &bytes.Buffer{}
	cmd.Stdout = buffer

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("unable to run command: %w", err)
	}

	var out output
	if err := json.NewDecoder(buffer).Decode(&out); err != nil {
		return "", fmt.Errorf("unable to decode JSON: %w", err)
	}

	if len(out.Streams) == 0 {
		return "", errors.New("streams length is 0")
	}

	width := out.Streams[0].Width
	height := out.Streams[0].Height

	const ratio16by9 = float64(16) / 9
	const ratio9by16 = float64(9) / 16
	const toleranceRange = 0.05

	ratio := float64(width) / float64(height)

	if ratio > ratio16by9-toleranceRange && ratio < ratio16by9+toleranceRange {
		return "16:9", nil
	}
	if ratio > ratio9by16-toleranceRange && ratio < ratio9by16+toleranceRange {
		return "9:16", nil
	}

	return "other", nil
}

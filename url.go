package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)

	req, err := presignClient.PresignGetObject(context.Background(),
		&s3.GetObjectInput{
			Bucket: ToPtr(bucket),
			Key:    ToPtr(key),
		},
		s3.WithPresignExpires(expireTime),
	)
	if err != nil {
		return "", fmt.Errorf("error making presigned HTTP request: %w", err)
	}

	return req.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	videoURLParts := strings.Split(*video.VideoURL, ",")
	bucket, key := videoURLParts[0], videoURLParts[1]

	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Second*5)
	if err != nil {
		return database.Video{}, fmt.Errorf("error generating presigned URL: %w", err)
	}

	video.VideoURL = &presignedURL

	return video, nil
}

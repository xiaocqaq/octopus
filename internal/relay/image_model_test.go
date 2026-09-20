package relay

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"testing"
)

func TestRewriteImageMultipartModel(t *testing.T) {
	var input bytes.Buffer
	writer := multipart.NewWriter(&input)
	if err := writer.WriteField("model", "group-name"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("image", "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("image-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	body, contentType, err := rewriteImageMultipartModel(input.Bytes(), writer.FormDataContentType(), "provider-model")
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type=%q err=%v", contentType, err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var modelValue, fileValue string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		switch part.FormName() {
		case "model":
			modelValue = string(value)
		case "image":
			fileValue = string(value)
		}
	}
	if modelValue != "provider-model" || fileValue != "image-bytes" {
		t.Fatalf("multipart fields model=%q file=%q", modelValue, fileValue)
	}
}

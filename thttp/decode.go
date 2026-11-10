package thttp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport/codec"
)

const defaultMaxRequestBodyBytes = 10 << 20

const supportedRequestContentTypes = "application/json, application/x-www-form-urlencoded, multipart/form-data"

const requestMediaTypeJSON = "application/json"

type unsupportedContentTypeError struct {
	contentType string
}

func (err *unsupportedContentTypeError) Error() string {
	return fmt.Sprintf("unsupported or malformed Content-Type %q; supported types: %s", err.contentType, supportedRequestContentTypes)
}

func requestMediaType(request *http.Request) (string, error) {
	if request == nil {
		return requestMediaTypeJSON, nil
	}
	rawContentType := strings.TrimSpace(request.Header.Get("Content-Type"))
	if rawContentType == "" {
		// Preserve compatibility for clients that send a JSON body without a
		// Content-Type header.
		return requestMediaTypeJSON, nil
	}

	mediaType, parameters, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return "", &unsupportedContentTypeError{contentType: rawContentType}
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case requestMediaTypeJSON, "application/x-www-form-urlencoded":
		return mediaType, nil
	case "multipart/form-data":
		if strings.TrimSpace(parameters["boundary"]) == "" {
			return "", &unsupportedContentTypeError{contentType: rawContentType}
		}
		return mediaType, nil
	default:
		return "", &unsupportedContentTypeError{contentType: rawContentType}
	}
}

func decodeRequestWithMediaType(request *http.Request, target any, requestCodec codec.Codec, maxBodyBytes int64, mediaType string) error {
	if request == nil || request.Body == nil || request.Body == http.NoBody {
		return nil
	}
	if requestCodec == nil {
		requestCodec = codec.Default
	}
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxRequestBodyBytes
	}

	switch mediaType {
	case "application/x-www-form-urlencoded":
		if err := request.ParseForm(); err != nil {
			return xerr.BadRequest("invalid form request body", err)
		}
		return BindFromTags(target, request)
	case "multipart/form-data":
		if err := request.ParseMultipartForm(maxBodyBytes); err != nil {
			return xerr.BadRequest("invalid multipart request body", err)
		}
		return BindFromTags(target, request)
	case requestMediaTypeJSON:
		return decodeRequestBody(request, target, requestCodec)
	default:
		return &unsupportedContentTypeError{contentType: mediaType}
	}
}

func requestHasBody(request *http.Request) (bool, error) {
	if request == nil || request.Body == nil || request.Body == http.NoBody ||
		request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
		return false, nil
	}
	if request.ContentLength == 0 {
		return false, nil
	}
	if request.ContentLength > 0 {
		return true, nil
	}

	originalBody := request.Body
	reader := bufio.NewReader(originalBody)
	_, err := reader.Peek(1)
	request.Body = &replayBody{Reader: reader, Closer: originalBody}
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, xerr.BadRequest("failed reading request body", err)
	}
	return true, nil
}

type replayBody struct {
	io.Reader
	io.Closer
}

func decodeRequestBody(request *http.Request, target any, requestCodec codec.Codec) error {
	if requestCodec.Name() == "json" {
		decoder := requestCodec.NewDecoder(request.Body)
		if err := decoder.Decode(target); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return xerr.BadRequest(fmt.Sprintf("invalid %s payload: %v", requestCodec.Name(), err), err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values in request body")
			}
			return xerr.BadRequest(fmt.Sprintf("invalid %s payload: %v", requestCodec.Name(), err), err)
		}
		return nil
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return xerr.BadRequest("failed reading request body", err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := requestCodec.Unmarshal(body, target); err != nil {
		return xerr.BadRequest(fmt.Sprintf("invalid %s payload: %v", requestCodec.Name(), err), err)
	}
	return nil
}

package thttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

type handlerMode uint8

const (
	modeTyped handlerMode = iota
	modeRaw
)

//nolint:gocyclo // HTTP handler branches mirror request lifecycle and preserve existing codec behavior.
func (t *Transport) httpHandler(ex action.Executable, meta *action.Meta, mode handlerMode) http.HandlerFunc {
	var typedResponse bool
	if typed, ok := ex.(action.TypedPayload); ok {
		resType := typed.ResPayload()
		typedResponse = isTypedHTTPResponse(resType)
	}

	return func(writer http.ResponseWriter, request *http.Request) {
		ctx := request.Context()
		requestID := xctx.RequestIDFrom(ctx)
		if requestID == "" {
			requestID = request.Header.Get(transport.HeaderRequestID)
		}
		if requestID != "" {
			writer.Header().Set(transport.HeaderRequestID, requestID)
		}
		if executionID := request.Header.Get(transport.HeaderExecutionID); executionID != "" {
			ctx = action.WithExecutionID(ctx, executionID)
			writer.Header().Set(transport.HeaderExecutionID, executionID)
		}
		traceID, spanID := request.Header.Get(transport.HeaderTraceID), request.Header.Get(transport.HeaderSpanID)
		if traceID != "" || spanID != "" {
			ctx = action.WithTraceContext(ctx, traceID, spanID)
		}
		if mode == modeRaw {
			ctx = WithRawHTTPRequest(ctx, request.WithContext(ctx))
		}

		result, err := ex.ExecuteDecoded(ctx, func(value any) error {
			if mode == modeRaw {
				return nil
			}
			if request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions {
				if err := BindFromTags(value, request); err != nil {
					return err
				}
				return BindMapFromHTTPRequest(value, request)
			}
			hasBody, err := requestHasBody(request)
			if err != nil {
				return err
			}
			if !hasBody {
				if bindErr := BindFromTags(value, request); bindErr != nil {
					return bindErr
				}
				return BindMapFromHTTPRequest(value, request)
			}

			request.Body = http.MaxBytesReader(writer, request.Body, t.maxBodyBytes)
			mediaType, err := requestMediaType(request)
			if err != nil {
				return err
			}
			isFormBody := mediaType == "application/x-www-form-urlencoded" || mediaType == "multipart/form-data"
			if isFormBody {
				if err := decodeRequestWithMediaType(request, value, t.codec, t.maxBodyBytes, mediaType); err != nil {
					return err
				}
			} else if err := BindFromTags(value, request); err != nil {
				return err
			}
			if err := BindMapFromHTTPRequest(value, request); err != nil {
				return err
			}
			if decoder, ok := value.(HTTPDecoder); ok {
				return decoder.FromHTTPRequest(request)
			}
			if isFormBody {
				return nil
			}
			return decodeRequestWithMediaType(request, value, t.codec, t.maxBodyBytes, mediaType)
		})
		if err != nil {
			if mode == modeRaw {
				slog.ErrorContext(ctx, "thttp: raw HTTP action failed", "error", err, "action", actionName(meta), "request_id", requestID)
				http.Error(writer, "internal server error", http.StatusInternalServerError)
				return
			}
			if unsupported, ok := errors.AsType[*unsupportedContentTypeError](err); ok {
				err = &xerr.AppError{Kind: xerr.KindUnsupportedMediaType, Message: unsupported.Error()}
			}
			writeActionError(writer, request, t.codec, err, requestID, 0)
			return
		}

		if mode == modeRaw {
			tracked := &trackedResponseWriter{ResponseWriter: writer}
			writeRawHTTPResponse(ctx, tracked, result, actionName(meta))
			return
		}

		if typedResponse {
			fallback := http.StatusOK
			if meta != nil && meta.SuccessStatus != 0 {
				fallback = meta.SuccessStatus
			}
			tracked := &trackedResponseWriter{ResponseWriter: writer}
			if err := writeTypedHTTPResponse(tracked, result, fallback, request.Method); err != nil {
				slog.ErrorContext(ctx, "thttp: write typed HTTP response", "error", err, "action", actionName(meta), "request_id", requestID)
				if !tracked.committed {
					http.Error(writer, "internal server error", http.StatusInternalServerError)
					return
				}
			}
			return
		}

		writer.Header().Set("Content-Type", t.codec.ContentType())
		status := http.StatusOK
		if meta != nil && meta.SuccessStatus != 0 {
			status = meta.SuccessStatus
		}
		writer.WriteHeader(status)
		if encodeErr := t.codec.NewEncoder(writer).Encode(result); encodeErr != nil {
			slog.ErrorContext(ctx, "thttp: write HTTP response", "error", encodeErr, "action", actionName(meta), "request_id", requestID)
		}
	}
}

func writeRawHTTPResponse(ctx context.Context, writer http.ResponseWriter, result any, name string) {
	var response RawHTTPResponse
	switch value := result.(type) {
	case RawHTTPResponse:
		response = value
	case *RawHTTPResponse:
		if value == nil {
			err := errors.New("raw action returned a nil *RawHTTPResponse")
			slog.ErrorContext(ctx, "thttp: invalid raw HTTP response", "error", err, "action", name)
			http.Error(writer, "internal server error", http.StatusInternalServerError)
			return
		}
		response = *value
	default:
		slog.ErrorContext(ctx, "thttp: invalid raw HTTP response type", "type", reflect.TypeOf(result), "action", name)
		http.Error(writer, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := response.validate(); err != nil {
		slog.ErrorContext(ctx, "thttp: invalid raw HTTP response", "error", err, "action", name)
		if tracked, ok := writer.(*trackedResponseWriter); !ok || !tracked.committed {
			http.Error(writer, "internal server error", http.StatusInternalServerError)
		}
		return
	}
	if err := response.WriteTo(writer); err != nil {
		slog.ErrorContext(ctx, "thttp: write raw HTTP response", "error", err, "action", name)
	}
}

func writeActionError(writer http.ResponseWriter, request *http.Request, responseCodec codec.Codec, err error, requestID string, statusOverride int) {
	if responseCodec == nil {
		responseCodec = codec.Default
	}
	appErr := xerr.From(err)
	status := transport.MapToHTTPStatus(appErr.Kind)
	if statusOverride != 0 {
		status = statusOverride
	}
	writer.Header().Set("Content-Type", responseCodec.ContentType())
	writer.WriteHeader(status)
	if encodeErr := responseCodec.NewEncoder(writer).Encode(appErr.Public(requestID)); encodeErr != nil {
		slog.ErrorContext(request.Context(), "thttp: write HTTP error response", "error", encodeErr, "request_id", requestID)
	}
}

func actionName(meta *action.Meta) string {
	if meta == nil {
		return ""
	}
	return meta.Name
}

type trackedResponseWriter struct {
	http.ResponseWriter
	committed bool
}

func (writer *trackedResponseWriter) WriteHeader(status int) {
	writer.committed = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *trackedResponseWriter) Write(body []byte) (int, error) {
	writer.committed = true
	return writer.ResponseWriter.Write(body)
}

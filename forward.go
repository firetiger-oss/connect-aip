package connectaip

import (
	"bytes"
	"io"
	"iter"
	"net/http"
	"net/url"
	"reflect"
	"strconv"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}
	marshalOpts   = protojson.MarshalOptions{
		AllowPartial:    true,
		Multiline:       true,
		Indent:          "  ",
		EmitUnpopulated: true,
	}
)

// Handle registers all handlers from the iterator with the mux.
func Handle(mux *http.ServeMux, handlers iter.Seq2[string, http.Handler]) {
	for pattern, handler := range handlers {
		mux.Handle(pattern, handler)
	}
}

type responseCapture struct {
	http.ResponseWriter
	body       bytes.Buffer
	statusCode int
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	return rc.body.Write(b)
}

func (rc *responseCapture) WriteHeader(statusCode int) {
	rc.statusCode = statusCode
}

func forward[Resp proto.Message](w http.ResponseWriter, req *http.Request, path string, handler http.Handler, reqMsg proto.Message) {
	reqBody, err := proto.Marshal(reqMsg)
	if err != nil {
		http.Error(w, "failed to marshal request: "+err.Error(), http.StatusBadRequest)
		return
	}

	internalReq := req.Clone(req.Context())
	internalReq.URL.Path = path
	internalReq.URL.RawPath = ""
	internalReq.URL.RawQuery = ""
	internalReq.Method = "POST"
	internalReq.Body = io.NopCloser(bytes.NewReader(reqBody))
	internalReq.ContentLength = int64(len(reqBody))
	internalReq.Header.Set("Content-Type", "application/proto")
	internalReq.Header.Del("Accept-Encoding")

	capture := &responseCapture{ResponseWriter: w, statusCode: http.StatusOK}
	handler.ServeHTTP(capture, internalReq)

	if capture.statusCode >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(capture.statusCode)
		w.Write(capture.body.Bytes())
		return
	}

	var zero Resp
	respMsg := reflect.New(reflect.TypeOf(zero).Elem()).Interface().(Resp)

	if err := proto.Unmarshal(capture.body.Bytes(), respMsg); err != nil {
		http.Error(w, "failed to unmarshal response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonBody, err := marshalOpts.Marshal(respMsg)
	if err != nil {
		http.Error(w, "failed to marshal JSON response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(capture.statusCode)
	w.Write(jsonBody)
}

// Forward clones the request, rewrites URL/method, and forwards to the ConnectRPC handler.
// Used for POST/PATCH with body: "*" and no path variables.
func Forward[Req, Resp proto.Message](w http.ResponseWriter, req *http.Request, path string, handler http.Handler) {
	var zeroReq Req
	reqMsg := reflect.New(reflect.TypeOf(zeroReq).Elem()).Interface().(Req)

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := unmarshalOpts.Unmarshal(bodyBytes, reqMsg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	forward[Resp](w, req, path, handler, reqMsg)
}

// ForwardWithBody clones the request, applies path vars and query params, and forwards.
// Used for GET/DELETE requests.
func ForwardWithBody[Req, Resp proto.Message](w http.ResponseWriter, req *http.Request, path string, handler http.Handler, reqMsg Req) {
	applyQueryParams(reqMsg, req.URL.Query())
	forward[Resp](w, req, path, handler, reqMsg)
}

// ForwardWithPathVars clones the request, merges path variables into the body, and forwards.
// Used for POST/PATCH with body: "*" and path variables.
func ForwardWithPathVars[Req, Resp proto.Message](w http.ResponseWriter, req *http.Request, path string, handler http.Handler, pathVarsMsg Req) {
	var zeroReq Req
	reqMsg := reflect.New(reflect.TypeOf(zeroReq).Elem()).Interface().(Req)

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := unmarshalOpts.Unmarshal(bodyBytes, reqMsg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	proto.Merge(reqMsg, pathVarsMsg)
	forward[Resp](w, req, path, handler, reqMsg)
}

func applyQueryParams(msg proto.Message, query url.Values) {
	refMsg := msg.ProtoReflect()
	fields := refMsg.Descriptor().Fields()

	for key, values := range query {
		if len(values) != 1 {
			continue
		}
		field := fields.ByJSONName(key)
		if field == nil {
			field = fields.ByName(protoreflect.Name(key))
		}
		if field == nil {
			continue
		}

		// Don't override fields already set (e.g., from path variables)
		if refMsg.Has(field) {
			continue
		}

		val := values[0]
		switch field.Kind() {
		case protoreflect.StringKind:
			refMsg.Set(field, protoreflect.ValueOfString(val))
		case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
			if i, err := strconv.ParseInt(val, 10, 32); err == nil {
				refMsg.Set(field, protoreflect.ValueOfInt32(int32(i)))
			}
		case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
			if i, err := strconv.ParseInt(val, 10, 64); err == nil {
				refMsg.Set(field, protoreflect.ValueOfInt64(i))
			}
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
			if i, err := strconv.ParseUint(val, 10, 32); err == nil {
				refMsg.Set(field, protoreflect.ValueOfUint32(uint32(i)))
			}
		case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			if i, err := strconv.ParseUint(val, 10, 64); err == nil {
				refMsg.Set(field, protoreflect.ValueOfUint64(i))
			}
		case protoreflect.BoolKind:
			if val == "true" || val == "false" {
				refMsg.Set(field, protoreflect.ValueOfBool(val == "true"))
			}
		case protoreflect.FloatKind:
			if f, err := strconv.ParseFloat(val, 32); err == nil {
				refMsg.Set(field, protoreflect.ValueOfFloat32(float32(f)))
			}
		case protoreflect.DoubleKind:
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				refMsg.Set(field, protoreflect.ValueOfFloat64(f))
			}
		}
	}
}

package veneur

import (
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type contextHandler func(c context.Context, w http.ResponseWriter, r *http.Request)

func (ch contextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// nil context is bad form, but since we don't use it, it's a quick hack
	// to allow us to write tests before we upgrade to Go 1.7 Context type
	// and the newer goji
	// TODO(aditya) actually update this
	ch(nil, w, r)
}

// handleImport generates the handler that responds to POST requests submitting
// metrics to the global veneur instance.
func handleImport(s *Server) http.Handler {
	return contextHandler(func(c context.Context, w http.ResponseWriter, r *http.Request) {
		innerLogger := s.logger.WithField("client", r.RemoteAddr)
		start := time.Now()

		var (
			jsonMetrics []JSONMetric
			body        io.ReadCloser
			err         error
			encoding    = r.Header.Get("Content-Encoding")
		)
		switch encLogger := innerLogger.WithField("encoding", encoding); encoding {
		case "":
			body = r.Body
			encoding = "identity"
		case "deflate":
			body, err = zlib.NewReader(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				encLogger.WithError(err).Error("Could not read compressed request body")
				s.statsd.Count("import.request_error_total", 1, []string{"cause:deflate"}, 1.0)
				return
			}
			defer body.Close()
		default:
			http.Error(w, encoding, http.StatusUnsupportedMediaType)
			encLogger.Error("Could not determine content-encoding of request")
			s.statsd.Count("import.request_error_total", 1, []string{"cause:unknown_content_encoding"}, 1.0)
			return
		}

		if err := json.NewDecoder(body).Decode(&jsonMetrics); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			innerLogger.WithError(err).Error("Could not decode /import request")
			s.statsd.Count("import.request_error_total", 1, []string{"cause:json"}, 1.0)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		s.statsd.TimeInMilliseconds("import.response_duration_ns",
			float64(time.Now().Sub(start).Nanoseconds()),
			[]string{"part:request", fmt.Sprintf("encoding:%s", encoding)},
			1.0)

		// the server usually waits for this to return before finalizing the
		// response, so this part must be done asynchronously
		go s.ImportMetrics(jsonMetrics)
	})
}

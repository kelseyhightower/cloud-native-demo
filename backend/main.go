// Copyright 2016 Google Inc. All Rights Reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"log"
	"net"
	"net/http"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	zipkin "github.com/openzipkin/zipkin-go-opentracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	listenIP     string
	listenPort   string
	advertisedIP string
	httpAddr     string
	serviceAddr  string
	zipkinURL    string
	tracer       opentracing.Tracer
)

const (
	defaultListenPort = "80"
	defaultListenIP   = "0.0.0.0"
	defaultZipkinURL  = "http://zipkin:9411/api/v1/spans"
)

var (
	httpRequestsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloud_native_app",
			Subsystem: "backend",
			Name:      "http_requests_total",
			Help:      "Number of HTTP requests",
		},
		[]string{"method", "status"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsCounter)
}

func main() {
	var err error

	flag.StringVar(&advertisedIP, "advertised-ip", "", "The advertised IP address")
	flag.StringVar(&listenIP, "listen-ip", defaultListenIP, "The HTTP bind IP address")
	flag.StringVar(&listenPort, "listen-port", defaultListenPort, "The HTTP port")
	flag.StringVar(&zipkinURL, "zipkin", defaultZipkinURL, "The Zipkin tracer URL")
	flag.Parse()

	httpAddr := net.JoinHostPort(listenIP, listenPort)
	serviceAddr := net.JoinHostPort(advertisedIP, listenPort)

	collector, err := zipkin.NewHTTPCollector(zipkinURL)
	if err != nil {
		log.Fatal(err)
	}

	recorder := zipkin.NewRecorder(collector, false, serviceAddr, "backend")
	tracer, err = zipkin.NewTracer(recorder, zipkin.TraceID128Bit(true))
	if err != nil {
		log.Fatal("unable to create Zipkin tracer: %+v", err)
	}
	opentracing.InitGlobalTracer(tracer)

	http.HandleFunc("/", handler)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(httpAddr, nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	var span opentracing.Span

	spanContext, err := opentracing.GlobalTracer().Extract(
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(r.Header),
	)

	if err != nil {
		log.Printf("error extracting opentracing headers: %v", err)
		httpRequestsCounter.WithLabelValues(r.Method, "500").Inc()
		http.Error(w, "service unavailable", 500)
		return
	}

	span = opentracing.StartSpan("backend", ext.RPCServerOption(spanContext))
	defer span.Finish()

	httpRequest, err := http.NewRequest(http.MethodGet, "https://google.com", nil)
	if err != nil {
		log.Printf("error creating new HTTP request: %v", err)
		httpRequestsCounter.WithLabelValues(r.Method, "500").Inc()
		http.Error(w, "service unavailable", 500)
		return
	}

	childSpan := opentracing.StartSpan(
		"get google.com",
		opentracing.ChildOf(span.Context()),
	)
	defer childSpan.Finish()

	resp, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		log.Printf("error running http request: %v", err)
		httpRequestsCounter.WithLabelValues(r.Method, "500").Inc()
		http.Error(w, "service unavailable", 500)
		return
	}
	resp.Body.Close()

	httpRequestsCounter.WithLabelValues(r.Method, "200").Inc()
}

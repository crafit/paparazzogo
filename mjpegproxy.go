// Copyright 2014 Jarmo Puttonen <jarmo.puttonen@gmail.com>. All rights reserved.
// Use of this source code is governed by a MIT-style
// licence that can be found in the LICENCE file.

package paparazzo.go

import (
	"bytes"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Mjpegproxy struct {
	curImg      bytes.Buffer
	curImgLock  sync.RWMutex
	conChan     chan time.Time
	running     bool
	partbufsize int
	imgbufsize  int
	l           net.Listener
}

func NewMjpegproxy() *Mjpegproxy {
	p := &Mjpegproxy{
		conChan: make(chan time.Time),
		// Max MJPEG-frame part size 1Mb
		partbufsize: 125000,
		// Max MJPEG-frame size 5Mb
		imgbufsize: 625000,
	}
	return p
}

func (m *Mjpegproxy) handlerfunc(w http.ResponseWriter, req *http.Request) {
	m.curImgLock.RLock()
	w.Write(m.curImg.Bytes())
	m.curImgLock.RUnlock()
	// Resume crawling when new connection
	select {
	case m.conChan <- time.Now():
	default:
	}
}

func (m *Mjpegproxy) serve(imgPath, laddr string) {
	http.HandleFunc(imgPath, m.handlerfunc)
	var err error
	m.l, err = net.Listen("tcp", laddr)
	if err != nil {
		log.Fatal(err)
	}
	err = http.Serve(m.l, nil)
	if err != nil {
		log.Fatal(err)
	}
}

func (m *Mjpegproxy) Serve(imgPath, laddr string) {
	go m.serve(imgPath, laddr)
}

func (m *Mjpegproxy) StopServing() {
	m.l.Close()
}

func (m *Mjpegproxy) StopCrawling() {
	m.running = false
}

func (m *Mjpegproxy) StartCrawling(mjpegStream, user, pass string, timeout time.Duration) {
	go m.startcrawling(mjpegStream, user, pass, timeout)
}

func (m *Mjpegproxy) startcrawling(mjpegStream, user, pass string, timeout time.Duration) {
	m.running = true
	client := new(http.Client)
	request, err := http.NewRequest("GET", mjpegStream, nil)
	if user != "" && pass != "" {
		request.SetBasicAuth(user, pass)
	}
	response, err := client.Do(request)

	var part *multipart.Part

	buffer := make([]byte, m.partbufsize)
	img := bytes.Buffer{}

	var lastconn time.Time

	for m.running == true {
		lastconn = <-m.conChan
		if m.running && (time.Since(lastconn) < timeout || timeout == 0) {
			response, err = client.Do(request)
			defer response.Body.Close()
			if err != nil {
				log.Fatalln(err.Error())
			}
			// Get boundary string from response and clean it up
			split := strings.Split(response.Header.Get("Content-Type"), "boundary=")
			boundary := split[1]
			// TODO: Find out what happens when boundarystring ends in --
			boundary = strings.Replace(boundary, "--", "", 1)

			reader := io.ReadCloser(response.Body)
			mpread := multipart.NewReader(reader, boundary)
			for m.running && (time.Since(lastconn) < timeout || timeout == 0) {
				part, err = mpread.NextPart()
				if err != nil {
					log.Fatalln(err.Error())
				}
				// Get parts until EOF-error or running=false
				for err == nil && m.running {
					amnt := 0
					amnt, err = part.Read(buffer)
					if err != nil && err.Error() != "EOF" {
						log.Fatalln(err.Error())
					}
					img.Write(buffer[0:amnt])
				}
				err = nil

				if img.Len() > m.imgbufsize {
					img.Truncate(m.imgbufsize)
				}
				m.curImgLock.Lock()
				m.curImg.Reset()
				_, err = m.curImg.Write(img.Bytes())
				if err != nil {
					m.curImgLock.Unlock()
					log.Fatalln(err.Error())
				}
				m.curImgLock.Unlock()
				img.Reset()
			}
		} else {
			response.Body.Close()
		}
	}
}

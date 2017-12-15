package regenbox

import (
	"errors"
	"fmt"
	"go.bug.st/serial.v1"
	"log"
	"sync"
	"time"
)

var ErrNoSerialPortFound = errors.New("didn't find any available serial port")
var ErrClosedPort = errors.New("serial port is closed")

var DefaultSerialConfig = serial.Mode{
	BaudRate: 57600,
	Parity:   serial.NoParity,
	DataBits: 8,
	StopBits: serial.OneStopBit,
}

var DefaultTimeout = time.Second

type SerialConnection struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	serial.Port
	path   string
	locked bool
	config serial.Mode

	rdChan    chan []byte
	wrChan    chan []byte
	errChan   chan error
	closeChan chan struct{}
	wg        sync.WaitGroup
}

func NewSerial(port serial.Port, config serial.Mode, name string, lockPort bool) *SerialConnection {
	return &SerialConnection{
		Port:      port,
		locked:    lockPort,
		path:      name,
		config:    config,
		rdChan:    make(chan []byte),
		wrChan:    make(chan []byte),
		errChan:   make(chan error),
		closeChan: make(chan struct{}),

		ReadTimeout:  DefaultTimeout,
		WriteTimeout: DefaultTimeout,
	}
}

// Start begins the two routines responsible
// for reading and writing on serial port.
func (sc *SerialConnection) Start() {
	sc.wg.Add(2)
	go func() {
		sc.readRoutine()
		sc.wg.Done()
	}()
	go func() {
		sc.writeRoutine()
		sc.wg.Done()
	}()
}

// Read takes one of sc.rdChan or sc.errChan, waiting up to sc.ReadTimeout,
// it also checks if connection is closed and returns error accordingly.
func (sc *SerialConnection) Read() (b []byte, err error) {
	select {
	case b = <-sc.rdChan:
		select {
		case err = <-sc.errChan:
		case <-sc.Closed():
		}
	case <-sc.Closed():
		err = ErrClosedPort
	case <-time.After(sc.ReadTimeout):
		err = fmt.Errorf("read timeout (%s)", sc.ReadTimeout)
	}
	return b, err
}

// Write pushes b to sc.wrChan, or returns an error
// after sc.WriteTimeout, or if connection is closed.
func (sc *SerialConnection) Write(b []byte) (err error) {
	select {
	case sc.wrChan <- b:
		select {
		case err = <-sc.errChan:
		case <-sc.Closed():
			err = ErrClosedPort
		}
	case <-sc.Closed():
		err = ErrClosedPort
	case <-time.After(sc.WriteTimeout):
		err = fmt.Errorf("write timeout (%s)", sc.WriteTimeout)
	}
	return err
}

// Close notifies read/write routines to stop, then waits
// for them to return, it then actually closes serial port.
func (sc *SerialConnection) Close() error {
	select {
	case <-sc.Closed():
		return ErrClosedPort
	default:
	}
	close(sc.closeChan)
	err := sc.Port.Close()
	sc.wg.Wait()
	return err
}

// Closed exposes <-sc.closeChan, which is closed if
// connection has been previously closed.
func (sc *SerialConnection) Closed() <-chan struct{} {
	return sc.closeChan
}

// Path returns device name / path of serial port.
func (sc *SerialConnection) Path() string {
	return sc.path
}

func (sc *SerialConnection) readRoutine() {
	for {
		b := make([]byte, 32)
		i, err := sc.Port.Read(b)

		// read until last byte is a stop
		j := i
		for err == nil && j > 0 && b[i-1] != StopByte {
			j, err = sc.Port.Read(b[i:])
			i += j
		}

		// do not send stop-byte
		if i > 0 && b[i-1] == StopByte {
			i -= 1
		}

		select {
		case sc.rdChan <- b[:i]:
		case <-sc.Closed():
			return
		}
		select {
		case sc.errChan <- err:
		case <-sc.Closed():
			return
		}
	}
}

func (sc *SerialConnection) writeRoutine() {
	var b []byte
	for {
		select {
		case b = <-sc.wrChan:
		case <-sc.Closed():
			return
		}
		_, err := sc.Port.Write(b)
		select {
		case sc.errChan <- err:
		case <-sc.Closed():
			return
		}
	}
}

// FindSerial tries to connect to first available serial port (platform independant hopefully).
// If config is nil, DefaultSerialMode is used.
func FindSerial(config *serial.Mode) (*SerialConnection, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	if config == nil {
		config = &DefaultSerialConfig
	}
	var port serial.Port
	for _, v := range ports {
		port, err = serial.Open(v, config)
		if err == nil {
			log.Printf("trying \"%s\"...", v)
			conn := NewSerial(port, *config, v, false)
			conn.ReadTimeout = time.Millisecond * 50
			conn.WriteTimeout = time.Millisecond * 50
			conn.Start()
			// create a temporary box to test connection
			rb := &RegenBox{Conn: conn, config: new(Config), state: Connected}
			t, err := rb.TestConnection()
			if err == nil {
				conn.ReadTimeout = DefaultTimeout
				conn.WriteTimeout = DefaultTimeout
				log.Printf("connected to \"%s\" in %s", v, t)
				return conn, nil
			} else {
				conn.Close()
				log.Printf("error connecting to \"%s\": %s", v, err)
			}
		}
	}
	if err == nil {
		return nil, ErrNoSerialPortFound
	}
	return nil, err
}

func OpenPortName(name string) (port serial.Port, config serial.Mode, err error) {
	config = DefaultSerialConfig
	port, err = serial.Open(name, &config)
	return port, config, err
}

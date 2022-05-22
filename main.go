package main

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/gordonklaus/portaudio"
	"gitlab.com/gomidi/midi/v2"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv" // autoregisters driver
)

var (
	flagUniqueStream = flag.Bool("unique-stream", false, "use a unique stream shared sample (needed when the platform supports)")
)

func main() {
	flag.Parse()
	if runtime.GOOS == "Linux" {
		*flagUniqueStream = true
	}

	defer midi.CloseDriver()

	portaudio.Initialize()
	defer portaudio.Terminate()

	hostAPI, err := portaudio.DefaultHostApi()
	check(err)
	dev := hostAPI.DefaultOutputDevice
	fmt.Printf("Default output: %s - %f Hz, max channels: %d\n", dev.Name, dev.DefaultSampleRate, dev.MaxOutputChannels)

	engine, err := NewEngine()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	ports := midi.InPorts()
	if len(ports) == 0 {
		fmt.Println("No MIDI input ports available")
		os.Exit(1)
	}
	for id, port := range ports {
		if id == 0 {
			fmt.Printf("Listening to MIDI port %s\n", port)
		} else {
			fmt.Println(id, port)
		}
	}
	// ---

	in := midi.FindInPort("Arturia BeatStep")
	if in < 0 {
		fmt.Println("can't find Arturia BeatStep")
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	moo, err := NewSample("sounds/cow_moo_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		moo.Close()
		os.Exit(1)
	}
	defer moo.Close()

	// -------------
	powerUp, err := NewSample("sounds/power_up_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		powerUp.Close()
		os.Exit(1)
	}
	defer powerUp.Close()

	powerUp.Play(&sig)

	// -------------
	powerDown, err := NewSample("sounds/power_down_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		powerDown.Close()
		os.Exit(1)
	}
	defer powerDown.Close()

	// -------------
	fart, err := NewSample("sounds/cow_fart_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		fart.Close()
		os.Exit(1)
	}
	defer fart.Close()

	// -------------

	bleep, err := NewSample("sounds/bleep_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load %s - %v\n", bleep.Path, err)
		bleep.Close()
		os.Exit(1)
	}
	defer bleep.Close()

	// -------------
	scan, err := NewSample("sounds/scan_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		scan.Close()
		os.Exit(1)
	}
	defer scan.Close()

	// -------------
	screenBeeps, err := NewSample("sounds/ScreenBeeps_32b.wav", engine)
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		screenBeeps.Close()
		os.Exit(1)
	}
	defer screenBeeps.Close()

	// -------------
	defaultDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		fmt.Println("Failed to open the default output device:", err)
		os.Exit(1)
	}
	fmt.Println("Default output device used:", defaultDevice.Name)
	defaultDevice.DefaultSampleRate = 48000

	stop, err := midi.ListenTo(in, func(msg midi.Message, timestampms int32) {
		var bt []byte
		var ch, key, vel uint8
		switch {
		case msg.GetSysEx(&bt):
			fmt.Printf("got sysex: % X\n", bt)
			// Stop control
			if bytes.Compare(bt, []byte{0x7F, 0x7F, 0x06, 0x01}) == 0 {
				fmt.Println("got a stop command")
				sig <- os.Interrupt
			}
		case msg.GetNoteStart(&ch, &key, &vel):

			fmt.Printf("starting note %d [%s] on channel %v with velocity %v\n", key, midi.Note(key), ch, vel)
			switch key {
			case 44: // pad 1
				go moo.Play(&sig)
			case 45: // pad 2
				go fart.Play(&sig)
			default:
				if rand.Intn(10)%2 == 0 {
					go screenBeeps.Play(&sig)
				}
				go scan.Play(&sig)
				go bleep.Play(&sig)
			}
		case msg.GetNoteEnd(&ch, &key):
			fmt.Printf("ending note %s on channel %v\n", midi.Note(key), ch)
		default:
			// ignore
		}
	}, midi.UseSysEx())

	if err != nil {
		fmt.Printf("ERROR: %s\n", err)
		return
	}

	fmt.Println("Listening to MIDI...input")
	bleep.Play(&sig)

	for {
		select {
		case <-sig:
			stop()
			powerDown.Play(&sig)
			time.Sleep(1 * time.Second)
			return
		default:
		}
		time.Sleep(150 * time.Millisecond)
	}

}

func check(err error) {
	if err != nil {
		fmt.Println("Error check triggered a panic:", err)
		panic(err)
	}
}

type Sample struct {
	file           *os.File
	Path           string
	Decoder        *wav.Decoder
	Mutex          sync.Mutex
	Buffer         []int32
	Stream         *portaudio.Stream
	Engine         *Engine
	decodingBuffer *audio.IntBuffer
}

func NewSample(path string, engine *Engine) (sample *Sample, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s - %w", path, err)
	}
	sample = &Sample{
		file:    f,
		Path:    path,
		Decoder: wav.NewDecoder(f),
		Buffer:  make([]int32, 8192),
		Engine:  engine,
	}
	if !sample.Decoder.IsValidFile() {
		return sample, fmt.Errorf("Not a valid WAV file")
	}
	// some platforms don't support multiple streams
	if !*flagUniqueStream {
		sample.Stream, err = portaudio.OpenDefaultStream(0, int(sample.Decoder.NumChans),
			float64(sample.Decoder.SampleRate), len(sample.Buffer), &sample.Buffer)
		if err != nil {
			err = fmt.Errorf("failed to open stream for path %s, with channels: %d, sample rate: %d, buffer length: %d - %w", sample.Path, sample.Decoder.NumChans, sample.Decoder.SampleRate, len(sample.Buffer), err)
		}
	}
	return sample, err
}

func (s *Sample) Close() {
	if s.file != nil {
		s.file.Close()
	}
	if s.Stream != nil {
		if _, err := s.Stream.AvailableToWrite(); err != nil {
			s.Stream.Close()
		}
	}

}

func (sample *Sample) Play(ch *chan (os.Signal)) {
	sample.Mutex.Lock()
	defer sample.Mutex.Unlock()

	// if our sample doesn't have a stream, we use the engine to play it.
	if sample.Stream == nil {
		fmt.Println("Playing", sample.Path, "via a unique stream")
		err := sample.Engine.PlaySample(sample, ch)
		if err != nil {
			fmt.Println("Failed to play sample:", err)
		}
		return
	}

	if sample.decodingBuffer == nil {
		sample.decodingBuffer = &audio.IntBuffer{Format: sample.Decoder.Format(), Data: make([]int, len(sample.Buffer))}
	}

	n := 42
	var err error

	if err := sample.Stream.Start(); err != nil {
		fmt.Printf("failed to start the stream for sample %s: %v\n", sample.Path, err)
	}

	defer sample.Stream.Stop()

	for n > 0 && err == nil {
		n, err = sample.Decoder.PCMBuffer(sample.decodingBuffer)
		// convert buf to a slice of int32 values
		for i := range sample.decodingBuffer.Data {
			sample.Buffer[i] = int32(sample.decodingBuffer.Data[i])
		}
		err = sample.Stream.Write()
		if err != nil {
			fmt.Printf("failed to write sample %s to stream: %v\n", sample.Path, err)
		}
		select {
		case <-*ch:
			return
		default:
		}
	}
	if err != nil {
		fmt.Println("failed to read the PCM buffer", err)
	} else {
		sample.Decoder.Rewind()
	}
}

// Some platforms don't support multiple streams on the same device,
// in that case, we need to use a unique stream.
// The engine stream is set to stereo/48KHz and can only play 1 sample at a time.
type Engine struct {
	Stream *portaudio.Stream
	mutex  sync.Mutex
	Buffer []int32
}

func NewEngine() (engine *Engine, err error) {
	engine = &Engine{
		Buffer: make([]int32, 8192),
	}
	engine.Stream, err = portaudio.OpenDefaultStream(0, 2, 48000, len(engine.Buffer), &engine.Buffer)
	return engine, err
}

func (e *Engine) PlaySample(sample *Sample, ch *chan (os.Signal)) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if err := e.Stream.Start(); err != nil {
		return fmt.Errorf("failed to start the stream for sample %s: %w\n", sample.Path, err)
	}

	defer e.Stream.Stop()

	n := 42
	var err error

	buffer := &audio.IntBuffer{Format: sample.Decoder.Format(), Data: make([]int, len(e.Buffer))}

	for n > 0 && err == nil {
		n, err = sample.Decoder.PCMBuffer(buffer)
		// convert buf to a slice of int32 values
		for i := range buffer.Data {
			e.Buffer[i] = int32(buffer.Data[i])
		}
		err = e.Stream.Write()
		if err != nil {
			err = fmt.Errorf("failed to write sample %s to stream: %v\n", sample.Path, err)
		}
		select {
		case <-*ch:
			fmt.Println("exit early")
			return nil
		default:
		}
	}

	if err != nil {
		err = fmt.Errorf("failed to read the PCM buffer: %w", err)
	} else {
		sample.Decoder.Rewind()
	}
	return err
}

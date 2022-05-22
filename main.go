package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/gordonklaus/portaudio"
	"gitlab.com/gomidi/midi/v2"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv" // autoregisters driver
)

var (
	streamMutex  sync.Mutex
	stream2Mutex sync.Mutex
)

func main() {
	defer midi.CloseDriver()

	portaudio.Initialize()
	defer portaudio.Terminate()

	hostAPI, err := portaudio.DefaultHostApi()
	check(err)
	dev := hostAPI.DefaultOutputDevice
	fmt.Printf("Default output: %s - %f Hz, max channels: %d\n", dev.Name, dev.DefaultSampleRate, dev.MaxOutputChannels)

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

	moo, err := NewSample("sounds/cow_moo_32b.wav")
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		moo.Close()
		os.Exit(1)
	}
	defer moo.Close()

	// -------------
	powerUp, err := NewSample("sounds/power_up_32b.wav")
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		os.Exit(1)
	}
	defer powerUp.Close()

	powerUp.Play(&sig)

	// -------------
	powerDown, err := NewSample("sounds/power_down_32b.wav")
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		os.Exit(1)
	}
	defer powerDown.Close()

	// -------------
	fart, err := NewSample("sounds/cow_fart_32b.wav")
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		fart.Close()
		os.Exit(1)
	}
	defer fart.Close()

	// -------------

	bleep, err := NewSample("sounds/bleep_32b.wav")
	if err != nil {
		fmt.Printf("failed to load %s - %v\n", bleep.Path, err)
		os.Exit(1)
	}
	defer bleep.Close()

	// -------------
	scan, err := NewSample("sounds/scan_32b.wav")
	if err != nil {
		fmt.Printf("failed to load sound - %v\n", err)
		scan.Close()
		os.Exit(1)
	}
	defer scan.Close()

	// -------------
	screenBeeps, err := NewSample("sounds/ScreenBeeps_32b.wav")
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
	fmt.Println("Default output device:", defaultDevice.Name)

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

func playAudioFile(sample *Sample, stream *portaudio.Stream, sig *chan (os.Signal)) {
	fmt.Println("trying to play something")
	sample.Mutex.Lock()
	defer sample.Mutex.Unlock()
	check(stream.Start())

	defer stream.Stop()

	buf := &audio.IntBuffer{Format: sample.Decoder.Format(), Data: make([]int, len(sample.Buffer))}
	n, err := sample.Decoder.PCMBuffer(buf)
	//fmt.Println(dec.BitDepth)
	// Assuming 32bit audio for now
	for i := range buf.Data {
		sample.Buffer[i] = int32(buf.Data[i])
	}
	if err = stream.Write(); err != nil {
		fmt.Println(err)
		time.Sleep(150 * time.Millisecond)
		if err = stream.Stop(); err != nil {
			fmt.Println(err)
			return
		}
		stream.Start()
		if err = stream.Write(); err != nil {
			fmt.Println(err)
			return
		}
	}

	for n > 0 && err == nil {
		n, err = sample.Decoder.PCMBuffer(buf)
		// convert buf to a slice of int32 values
		for i := range buf.Data {
			sample.Buffer[i] = int32(buf.Data[i])
		}
		check(stream.Write())
		select {
		case <-*sig:
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

func check(err error) {
	if err != nil {
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
	decodingBuffer *audio.IntBuffer
}

func (s *Sample) Close() {
	s.file.Close()
	s.Stream.Close()
}

func (sample *Sample) Play(sig *chan (os.Signal)) {
	sample.Mutex.Lock()
	defer sample.Mutex.Unlock()
	check(sample.Stream.Start())

	defer sample.Stream.Stop()

	n, err := sample.Decoder.PCMBuffer(sample.decodingBuffer)
	//fmt.Println(dec.BitDepth)
	// Assuming 32bit audio for now
	for i := range sample.decodingBuffer.Data {
		sample.Buffer[i] = int32(sample.decodingBuffer.Data[i])
	}
	if err = sample.Stream.Write(); err != nil {
		fmt.Println(err)
		time.Sleep(150 * time.Millisecond)
		if err = sample.Stream.Stop(); err != nil {
			fmt.Println(err)
			return
		}
		sample.Stream.Start()
		if err = sample.Stream.Write(); err != nil {
			fmt.Println(err)
			return
		}
	}

	for n > 0 && err == nil {
		n, err = sample.Decoder.PCMBuffer(sample.decodingBuffer)
		// convert buf to a slice of int32 values
		for i := range sample.decodingBuffer.Data {
			sample.Buffer[i] = int32(sample.decodingBuffer.Data[i])
		}
		check(sample.Stream.Write())
		select {
		case <-*sig:
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

func NewSample(path string) (sample *Sample, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	sample = &Sample{
		file:    f,
		Path:    path,
		Decoder: wav.NewDecoder(f),
		Buffer:  make([]int32, 8192),
	}
	if !sample.Decoder.IsValidFile() {
		return sample, fmt.Errorf("Not a valid WAV file")
	}
	sample.decodingBuffer = &audio.IntBuffer{Format: sample.Decoder.Format(), Data: make([]int, len(sample.Buffer))}
	sample.Stream, err = portaudio.OpenDefaultStream(0, int(sample.Decoder.NumChans),
		float64(sample.Decoder.SampleRate), len(sample.Buffer), &sample.Buffer)
	return sample, err
}

/*
Package fasta contains fasta parsers and writers.

Fasta is a flat text file format developed in 1985 to store nucleotide and
amino acid sequences. It is extremely simple and well-supported across many
languages. However, this simplicity means that annotation of genetic objects
is not supported.

This package provides a parser and writer for working with Fasta formatted
genetic sequences.
*/
package fasta

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"unsafe"
)

/******************************************************************************
Apr 25, 2021

Fasta Parser begins here

Many thanks to Jordan Campbell (https://github.com/0x106) for building the first
parser for Poly and thanks to Tim Stiles (https://github.com/TimothyStiles)
for helping complete that PR. This work expands on the previous work by allowing
for concurrent  parsing and giving Poly a specific  parser subpackage,
as well as few bug fixes.

Fasta is a very simple file format for working with DNA, RNA, or protein sequences.
It was first released in 1985 and is still widely used in bioinformatics.

https://en.wikipedia.org/wiki/_format

One interesting use of the concurrent  parser is working with the Uniprot
fasta dump files, which are far too large to fit into RAM. This parser is able
to easily handle those files by doing computation actively while the data dump
is getting parsed.

https://www.uniprot.org/downloads

I have removed the  Parsers from the io.go file and moved them into this
subpackage.

Hack the Planet,

Keoni

******************************************************************************/

var (
	gzipReaderFn = gzip.NewReader
	openFn       = os.Open
	buildFn      = Build
)

// Fasta is a struct representing a single Fasta file element with a Name and its corresponding Sequence.
type Fasta struct {
	Name     string `json:"name"`
	Sequence string `json:"sequence"`
}

// Parse parses a given Fasta file into an array of Fasta structs. Internally, it uses ParseFastaConcurrent.
func Parse(r io.Reader) ([]Fasta, error) {
	// 32kB is a magic number often used by the Go stdlib for parsing. We multiply it by two.
	const maxLineSize = 2 * 32 * 1024
	parser := NewParser(r, maxLineSize)
	return parser.ParseAll()
}

// Parser is a flexible parser that provides ample
// control over reading fasta-formatted sequences.
// It is initialized with NewParser.
type Parser struct {
	// reader keeps state of current reader.
	reader bufio.Reader
	line   uint
}

// NewParser returns a Parser that uses r as the source
// from which to parse fasta formatted sequences.
func NewParser(r io.Reader, maxLineSize int) *Parser {
	return &Parser{
		reader: *bufio.NewReaderSize(r, maxLineSize),
	}
}

// ParseAll parses all sequences in underlying reader only returning non-EOF errors.
// It returns all valid fasta sequences up to error if encountered.
func (parser *Parser) ParseAll() ([]Fasta, error) {
	return parser.ParseN(math.MaxInt)
}

// ParseN parses up to maxSequences fasta sequences from the Parser's underlying reader.
// ParseN does not return EOF if encountered.
// If an non-EOF error is encountered it returns it and all correctly parsed sequences up to then.
func (parser *Parser) ParseN(maxSequences int) (fastas []Fasta, err error) {
	for counter := 0; counter < maxSequences; counter++ {
		fasta, _, err := parser.ParseNext()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil // EOF not treated as parsing error.
			}
			return fastas, err
		}
		fastas = append(fastas, fasta)
	}
	return fastas, nil
}

// ParseByteLimited parses fastas until byte limit is reached.
// This is NOT a hard limit. To set a hard limit on bytes read use a
// io.LimitReader to wrap the reader passed to the Parser.
func (parser *Parser) ParseByteLimited(byteLimit int64) (fastas []Fasta, bytesRead int64, err error) {
	for bytesRead < byteLimit {
		fasta, n, err := parser.ParseNext()
		bytesRead += n
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil // EOF not treated as parsing error.
			}
			return fastas, bytesRead, err
		}
		fastas = append(fastas, fasta)
	}
	return fastas, bytesRead, nil
}

// ParseNext reads next fasta genome in underlying reader and returns the result
// and the amount of bytes read during the call.
// ParseNext only returns an error if it:
//   - Attempts to read and fails to find a valid fasta sequence.
//   - Returns reader's EOF if called after reader has been exhausted.
//   - If a EOF is encountered immediately after a sequence with no newline ending.
//     In this case the Fasta up to that point is returned with an EOF error.
//
// It is worth noting the amount of bytes read are always right up to before
// the next fasta starts which means this function can effectively be used
// to index where fastas start in a file or string.
func (parser *Parser) ParseNext() (Fasta, int64, error) {
	if _, err := parser.reader.Peek(1); err != nil {
		// Early return on error. Probably will be EOF.
		return Fasta{}, 0, err
	}
	// Initialization of parser state variables.
	var (
		// Parser looks for a line starting with '>' (U+003E)
		// that contains the next fasta sequence name.
		lookingForName = true
		seqName        string
		sequence, line []byte
		err            error
		totalRead      int64
	)

	// parse loop begins here.
	for {
		line, err = parser.reader.ReadSlice('\n')
		isSkippable := len(line) <= 1 || line[0] == ';' // OR short circuits so no panic here.
		totalRead += int64(len(line))
		parser.line++

		// More general case of error handling.
		if err != nil {
			isEOF := errors.Is(err, io.EOF)
			if isSkippable {
				if isEOF {
					// got EOF on a empty or commented line.
					err = nil
				}
				break
			} else if errors.Is(err, bufio.ErrBufferFull) {
				// Buffer size too small to read fasta line.
				return Fasta{}, totalRead, fmt.Errorf("line %d too large for buffer, use larger maxLineSize: %w", parser.line+1, err)
			} else if !isEOF {
				return Fasta{}, totalRead, err // Unexpected error.
			}

			// So got to this point the line is probably OK, we will return a Fasta.
			// with the EOF error.
			sequence = append(sequence, line...)
			break
		}

		line = line[:len(line)-1] // Exclude newline delimiter.
		peek, _ := parser.reader.Peek(1)
		if !lookingForName && len(peek) == 1 && peek[0] == '>' {
			// We are currently parsing a fasta and next line contains a new fasta.
			// We handle this situation by appending current line to sequence if not a comment
			// and ending the current fasta parsing.
			if !isSkippable {
				sequence = append(sequence, line...)
			}
			break
		} else if isSkippable {
			continue
		}

		if lookingForName {
			if line[0] == '>' {
				// We got the start of a fasta.
				seqName = string(line[1:])
				lookingForName = false
			}
			// This continue will also skip line if we are looking for name
			// and the current line does not contain the name.
			continue
		}
		// If we got to this point we are currently inside of the fasta
		// sequence contents. We append line to what we found of sequence so far.
		sequence = append(sequence, line...)
	} // parse loop ends here.

	// Parsing ended. Check for inconsistencies.
	if lookingForName {
		return Fasta{}, totalRead, fmt.Errorf("did not find fasta start '>', got to line %d: %w", parser.line, err)
	}
	if !lookingForName && len(sequence) == 0 {
		// We found a fasta name but no sequence to go with it.
		return Fasta{}, totalRead, fmt.Errorf("empty fasta sequence for %q,  got to line %d: %w", seqName, parser.line, err)
	}
	fasta := Fasta{
		Name:     seqName,
		Sequence: *(*string)(unsafe.Pointer(&sequence)), // Stdlib strings.Builder.String() does this so it *should* be safe.
	}
	// Gotten to this point err is non-nil only in EOF case.
	// We report this error to note the fasta may be incomplete/corrupt
	// like in the case of using an io.LimitReader wrapping the underlying reader.
	// We return the fasta as well since some libraries generate fastas with no
	// ending newline i.e Zymo. It is up to the user to decide whether they want
	// an EOF-ended fasta or not, the rest of this library discards EOF-ended fastas.
	return fasta, totalRead, err
}

// Reset discards all data in buffer and resets state.
func (parser *Parser) Reset(r io.Reader) {
	parser.reader.Reset(r)
	parser.line = 0
}

// ParseConcurrent concurrently parses a given Fasta file in an io.Reader into a channel of Fasta structs.
func ParseConcurrent(r io.Reader, sequences chan<- Fasta) {
	// Initialize necessary variables
	var sequenceLines []string
	var name string
	start := true

	// Start the scanner
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		// if there's nothing on this line skip this iteration of the loop
		case len(line) == 0:
			continue
		// if it's a comment skip this line
		case line[0:1] == ";":
			continue
		// start of a fasta line
		case line[0:1] != ">":
			sequenceLines = append(sequenceLines, line)
		// Process normal new lines
		case line[0:1] == ">" && !start:
			sequence := strings.Join(sequenceLines, "")
			newFasta := Fasta{
				Name:     name,
				Sequence: sequence}
			// Reset sequence lines
			sequenceLines = []string{}
			// New name
			name = line[1:]
			sequences <- newFasta
		// Process first line of file
		case line[0:1] == ">" && start:
			name = line[1:]
			start = false
		}
	}
	// Add final sequence in file to channel
	sequence := strings.Join(sequenceLines, "")
	newFasta := Fasta{
		Name:     name,
		Sequence: sequence}
	sequences <- newFasta
	close(sequences)
}

/******************************************************************************

Start of  Read functions

******************************************************************************/

// ReadGzConcurrent concurrently reads a gzipped Fasta file into a Fasta channel.
// Deprecated: Use Parser.ParseNext() instead.
func ReadGzConcurrent(path string, sequences chan<- Fasta) {
	file, _ := os.Open(path) // TODO: these errors need to be handled/logged
	reader, _ := gzipReaderFn(file)
	go func() {
		defer file.Close()
		defer reader.Close()
		ParseConcurrent(reader, sequences)
	}()
}

// ReadConcurrent concurrently reads a flat Fasta file into a Fasta channel.
func ReadConcurrent(path string, sequences chan<- Fasta) {
	file, _ := os.Open(path) // TODO: these errors need to be handled/logged
	go func() {
		defer file.Close()
		ParseConcurrent(file, sequences)
	}()
}

// ReadGz reads a gzipped  file into an array of Fasta structs.
func ReadGz(path string) ([]Fasta, error) {
	file, err := openFn(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzipReaderFn(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return Parse(reader)
}

// Read reads a  file into an array of Fasta structs
func Read(path string) ([]Fasta, error) {
	file, err := openFn(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Parse(file)
}

/******************************************************************************

Start of  Write functions

******************************************************************************/

// Build converts a Fastas array into a byte array to be written to a file.
func Build(fastas []Fasta) ([]byte, error) {
	var fastaString bytes.Buffer
	fastaLength := len(fastas)
	for fastaIndex, fasta := range fastas {
		fastaString.WriteString(">")
		fastaString.WriteString(fasta.Name)
		fastaString.WriteString("\n")

		lineCount := 0
		// write the fasta sequence 80 characters at a time
		for _, character := range fasta.Sequence {

			fastaString.WriteRune(character)
			lineCount++
			if lineCount == 80 {
				fastaString.WriteString("\n")
				lineCount = 0
			}
		}
		if fastaIndex != fastaLength-1 {
			fastaString.WriteString("\n\n")
		}
	}
	return fastaString.Bytes(), nil
}

// Write writes a fasta array to a file.
func Write(fastas []Fasta, path string) error {
	fastaBytes, err := buildFn(fastas) //  fasta.Build returns only nil errors.
	if err != nil {
		return err
	}
	return os.WriteFile(path, fastaBytes, 0644)
}

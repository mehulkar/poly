/*
Package random provides functions to generate random DNA and protein sequences.
*/
package random

import (
	"errors"
	"math/rand"
)

// ProteinSequence returns a random protein sequence string of a given length and seed.
// All returned sequences start M (Methionine) and end with * (stop codon).
func ProteinSequence(length int, seed int64) (string, error) {
	//The length needs to be greater than two because the random protein sequenced returned always contain a start and stop codon. You could see more about this stuff here: https://en.wikipedia.org/wiki/Genetic_code#Start_and_stop_codons
	if length <= 2 {
		err := errors.New("The length needs to be greater than two because the random protein sequenced returned always contain a start and stop codon. Please select a higher length in RandomProteinSequence function")
		return "", err
	}

	// https://en.wikipedia.org/wiki/Amino_acid#Table_of_standard_amino_acid_abbreviations_and_properties
	var aminoAcidsAlphabet = []rune("ACDEFGHIJLMNPQRSTVWY")
	rand.Seed(seed)

	randomSequence := make([]rune, length)

	for peptide := range randomSequence {
		if peptide == 0 {
			//M is the standard abbreviation for the Methionine aminoacid. A protein sequence start with M because the start codon is translated to Methionine
			randomSequence[peptide] = 'M'
		} else if peptide == length-1 {
			//* is the standard abbreviation for the stop codon. That's a signal for the ribosome to stop the translation and because of that a protein sequence is finished with *
			randomSequence[peptide] = '*'
		} else {
			randomIndex := rand.Intn(len(aminoAcidsAlphabet))
			randomSequence[peptide] = aminoAcidsAlphabet[randomIndex]
		}
	}

	return string(randomSequence), nil
}

// DNASequence returns a random DNA sequence string of a given length and seed.
func DNASequence(length int, seed int64) (string, error) {

	var nucleicAcidsAlphabet = []rune("ACTG")
	alphabetLength := len(nucleicAcidsAlphabet)
	rand.Seed(seed)

	randomSequence := make([]rune, length)
	for basepair := range randomSequence {
		randomIndex := rand.Intn(alphabetLength)
		randomSequence[basepair] = nucleicAcidsAlphabet[randomIndex]
	}

	return string(randomSequence), nil
}

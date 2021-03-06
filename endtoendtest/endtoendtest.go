//
// endtoendtest.go
// Copyright(c)2015 Google, Inc.
//
// This file is part of skicka.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

/*
more things that need testing:
- single file uploads/downloads
- downloading Drive files/folders that weren't originally created by skicka
- sometimes randomly interrupt the upload?
*/

package main

import (
	crand "crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var encrypt bool

func main() {
	encrypt = os.Getenv("SKICKA_PASSPHRASE") != ""
	if encrypt {
		log.Printf("Found SKICKA_PASSPHRASE; testing with encryption")
	}

	// TODO: make it possible to set the seed from the command-line.
	seed := int64(os.Getpid())
	log.Printf("Seed = %d", seed)
	rand.Seed(seed)

	tmpSrc, err := ioutil.TempDir("", "skicka-test-src")
	if err != nil {
		log.Fatalf("%s", err)
	}
	log.Printf("Local src directory: %s", tmpSrc)

	tmpDst, err := ioutil.TempDir("", "skicka-test-dst")
	if err != nil {
		log.Fatalf("%s", err)
	}
	log.Printf("Local dst directory: %s", tmpDst)

	if err := prepDrive(); err != nil {
		log.Fatalf("%s", err)
	}

	iters := 20 // TODO: command line arg for this

	for i := 0; i < iters; i++ {
		if err := update(tmpSrc); err != nil {
			log.Fatalf("%s\n", err)
		}

		// skicka upload (possibly encrypt)
		if err := upload(tmpSrc); err != nil {
			log.Fatalf("%s\n", err)
		}

		// skicka download to second tmp dir
		if err := download(tmpDst); err != nil {
			log.Fatalf("%s\n", err)
		}

		err := compare(tmpSrc, tmpDst)
		if err != nil {
			log.Fatalf("%s", err)
		}
	}
}

const driveDir = "/skicka_test"

var nDirs = 1

func prepDrive() error {
	log.Printf("Removing %s on Drive", driveDir)
	cmd := exec.Command("skicka", "rm", "-r", driveDir)
	_ = cmd.Run()
	return nil
}

func randBool() bool {
	return rand.Float32() < .25
}

var createdFiles = make(map[string]bool)

func name(dir string) string {
	fodder := []string{"car", "house", "food", "cat", "monkey", "bird", "yellow",
		"blue", "fast", "sky", "table", "pen", "round", "book", "towel", "hair",
		"laugh", "airplane", "bannana", "tape", "round"}
	s := ""
	for {
		s += fodder[rand.Int31()%int32(len(fodder))]
		if _, ok := createdFiles[s]; !ok {
			break
		}
		s += "_"
	}
	createdFiles[s] = true
	return filepath.Join(dir, s)
}

func expSize() int64 {
	logSize := (rand.Int31() % 28) - 1
	s := int64(0)
	if logSize >= 0 {
		s = 1 << uint(logSize)
		s += rand.Int63() % s
	}
	return s
}

func update(dir string) error {
	filesLeftToCreate := 20
	dirsLeftToCreate := 5
	log.Printf("Updating %s", dir)

	return filepath.Walk(dir,
		func(path string, stat os.FileInfo, patherr error) error {
			if patherr != nil {
				return patherr
			}

			if stat.IsDir() {
				dirsToCreate := 0
				for i := 0; i < dirsLeftToCreate; i++ {
					if rand.Int31()%int32(nDirs) == 0 {
						dirsToCreate++
						n := name(path)
						err := os.Mkdir(n, 0700)
						log.Printf("%s: created directory", n)
						if err != nil {
							return err
						}
					}
				}
				nDirs += dirsToCreate
				dirsLeftToCreate -= dirsToCreate

				filesToCreate := 0
				for i := 0; i < filesLeftToCreate; i++ {
					if rand.Int31()%int32(nDirs) == 0 {
						filesToCreate++
						n := name(path)
						f, err := os.Create(n)
						if err != nil {
							return err
						}
						newlen := expSize()
						io.Copy(f, &io.LimitedReader{R: crand.Reader, N: int64(newlen)})
						f.Close()
						log.Printf("%s: created file. length %d", n, newlen)
					}
				}
				filesLeftToCreate -= filesToCreate

			}

			if randBool() {
				// Advance the modified time.  Don't go into the future.
				for {
					ms := rand.Int31() % 10000
					t := stat.ModTime().Add(time.Duration(ms) * time.Millisecond)
					if t.Before(time.Now()) {
						err := os.Chtimes(path, t, t)
						if err != nil {
							return err
						}
						log.Printf("%s: advanced modification time to %s", path, t.String())
						break
					}
				}
			}

			perms := stat.Mode()
			if randBool() {
				// change permissions
				newp := rand.Int31() & 0777
				if stat.IsDir() {
					newp |= 0700
				} else {
					newp |= 0400
				}

				err := os.Chmod(path, os.FileMode(newp))
				if err != nil {
					return err
				}
				log.Printf("%s: changed permissions to %#o", path, newp)
				perms = os.FileMode(newp)
			}

			if randBool() && !stat.IsDir() && (perms&0600) == 0600 {
				f, err := os.OpenFile(path, os.O_WRONLY, 0666)
				if err != nil {
					return err
				}
				defer f.Close()

				// seek somewhere and write some stuff
				offset := int64(0)
				if stat.Size() > 0 {
					offset = rand.Int63() % stat.Size()
				}

				b := make([]byte, expSize())
				_, err = io.ReadFull(crand.Reader, b)
				if err != nil {
					return err
				}
				_, err = f.WriteAt(b, offset)
				log.Printf("%s: wrote %d bytes at offset %d", path, len(b), offset)
				if err != nil {
					return err
				}

				if randBool() && stat.Size() > 0 {
					// truncate it as well
					sz := rand.Int63() % stat.Size()
					err := f.Truncate(int64(sz))
					if err != nil {
						return err
					}
					log.Printf("%s: truncated at %d", path, sz)
				}
			}

			return nil
		})
}

func upload(dir string) error {
	log.Printf("Starting upload")
	var cmd *exec.Cmd
	if encrypt {
		cmd = exec.Command("skicka", "upload", "-encrypt", dir, driveDir)
	} else {
		cmd = exec.Command("skicka", "upload", dir, driveDir)
	}
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func download(dir string) error {
	log.Printf("Starting download")
	cmd := exec.Command("skicka", "download", driveDir, dir)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func compare(patha, pathb string) error {
	mismatches := 0
	err := filepath.Walk(patha,
		func(pa string, stata os.FileInfo, patherr error) error {
			if patherr != nil {
				return patherr
			}

			// compute corresponding pathname for second file
			rest := pa[len(patha):]
			pb := filepath.Join(pathb, rest)

			statb, err := os.Stat(pb)
			if os.IsNotExist(err) {
				log.Printf("%s: not found\n", pb)
				mismatches++
				return nil
			}

			if stata.IsDir() != statb.IsDir() {
				log.Printf("%s: is file/is directory "+
					"mismatch with %s\n", pa, pb)
				mismatches++
				return nil
			}

			// compare permissions
			if stata.Mode() != statb.Mode() {
				log.Printf("%s: permissions %#o mismatch "+
					"%s permissions %#o\n", pa, stata.Mode(), pb, statb.Mode())
				mismatches++
			}

			// compare modification times
			// FIXME: there's a bug for directories, so only check files for now
			if !stata.IsDir() && stata.ModTime() != statb.ModTime() {
				log.Printf("%s: mod time %s mismatches "+
					"%s mod time %s\n", pa, stata.ModTime().String(),
					pb, statb.ModTime().String())
				mismatches++
			}

			// compare sizes
			if stata.Size() != statb.Size() {
				log.Printf("%s: size %d mismatches "+
					"%s size %d\n", pa, stata.Size(), pb, statb.Size())
				mismatches++
				return nil
			}

			// compare contents
			if !stata.IsDir() {
				cmp := exec.Command("cmp", pa, pb)
				err := cmp.Run()
				if err != nil {
					log.Printf("%s and %s differ", pa, pb)
					mismatches++
				}
			}
			return nil
		})

	if err != nil {
		return err
	} else if mismatches > 0 {
		return fmt.Errorf("%d file mismatches", mismatches)
	}
	return nil
}

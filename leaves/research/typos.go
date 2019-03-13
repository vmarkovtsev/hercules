package research

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/gogo/protobuf/proto"
	"github.com/sergi/go-diff/diffmatchpatch"
	"gopkg.in/bblfsh/sdk.v2/uast"
	"gopkg.in/bblfsh/sdk.v2/uast/nodes"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/hercules.v9/internal/core"
	"gopkg.in/src-d/hercules.v9/internal/levenshtein"
	"gopkg.in/src-d/hercules.v9/internal/pb"
	items "gopkg.in/src-d/hercules.v9/internal/plumbing"
	uast_items "gopkg.in/src-d/hercules.v9/internal/plumbing/uast"
)

// TyposDatasetBuilder collects pairs of typo-fix in source code identifiers.
type TyposDatasetBuilder struct {
	core.NoopMerger

	// MaximumAllowedDistance is the maximum Levenshtein distance between two identifiers
	// to consider them a typo-fix pair.
	MaximumAllowedDistance int

	// typos stores the found typo-fix pairs.
	typos []Typo
	// lcontext is the Context for measuring Levenshtein distance between lines.
	lcontext *levenshtein.Context
	// xpather filters identifiers.
	xpather uast_items.ChangesXPather
}

// TyposResult is returned by TyposDatasetBuilder.Finalize() and carries the found typo-fix
// pairs of identifiers.
type TyposResult struct {
	Typos []Typo
}

// Typo carries the information about a typo-fix pair.
type Typo struct {
	Wrong   string
	Correct string
	Commit  plumbing.Hash
	File    string
	Line    int
}

const (
	// DefaultMaximumAllowedTypoDistance is the default value of the maximum Levenshtein distance
	// between two identifiers to consider them a typo-fix pair.
	DefaultMaximumAllowedTypoDistance = 4
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (tdb *TyposDatasetBuilder) Name() string {
	return "TyposDataset"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (tdb *TyposDatasetBuilder) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (tdb *TyposDatasetBuilder) Requires() []string {
	arr := [...]string{
		uast_items.DependencyUastChanges, items.DependencyFileDiff, items.DependencyBlobCache}
	return arr[:]
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (tdb *TyposDatasetBuilder) ListConfigurationOptions() []core.ConfigurationOption {
	return nil
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (tdb *TyposDatasetBuilder) Configure(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (tdb *TyposDatasetBuilder) Flag() string {
	return "typos-dataset"
}

// Description returns the text which explains what the analysis is doing.
func (tdb *TyposDatasetBuilder) Description() string {
	return "Extracts typo-fix identifier pairs from source code in commit diffs."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (tdb *TyposDatasetBuilder) Initialize(repository *git.Repository) error {
	if tdb.MaximumAllowedDistance == 0 {
		tdb.MaximumAllowedDistance = DefaultMaximumAllowedTypoDistance
	}
	tdb.lcontext = &levenshtein.Context{}
	tdb.xpather.XPath = "//uast:Identifier"
	return nil
}

type candidate struct {
	Before int
	After  int
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (tdb *TyposDatasetBuilder) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if deps[core.DependencyIsMerge].(bool) {
		return nil, nil
	}
	commit := deps[core.DependencyCommit].(*object.Commit).Hash
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	diffs := deps[items.DependencyFileDiff].(map[string]items.FileDiffData)
	changes := deps[uast_items.DependencyUastChanges].([]uast_items.Change)
	for _, change := range changes {
		if change.Before == nil || change.After == nil {
			continue
		}
		linesBefore := bytes.Split(cache[change.Change.From.TreeEntry.Hash].Data, []byte{'\n'})
		linesAfter := bytes.Split(cache[change.Change.To.TreeEntry.Hash].Data, []byte{'\n'})
		diff := diffs[change.Change.To.Name]
		var lineNumBefore, lineNumAfter int
		clear := false
		var candidates []candidate
		focusedLinesBefore := map[int]bool{}
		focusedLinesAfter := map[int]bool{}
		for _, edit := range diff.Diffs {
			size := utf8.RuneCountInString(edit.Text)
			switch edit.Type {
			case diffmatchpatch.DiffDelete:
				lineNumBefore += size
				clear = size == 1
			case diffmatchpatch.DiffInsert:
				if size == 1 && clear {
					dist := tdb.lcontext.Distance(
						string(linesBefore[lineNumBefore-1]),
						string(linesAfter[lineNumAfter]))
					if dist <= tdb.MaximumAllowedDistance {
						candidates = append(candidates, candidate{lineNumBefore-1, lineNumAfter})
						focusedLinesBefore[lineNumBefore-1] = true
						focusedLinesAfter[lineNumAfter] = true
					}
				}
				lineNumAfter += size
				clear = false
			case diffmatchpatch.DiffEqual:
				lineNumBefore += size
				lineNumAfter += size
				clear = false
			}
		}
		if len(candidates) == 0 {
			continue
		}
		// at this point we have pairs of very similar lines
		// we need to build the line mappings of the identifiers before/after the change
		// we should keep only those which are present on those focused lines
		nodesAdded, nodesRemoved := tdb.xpather.Extract([]uast_items.Change{change})
		addedIdentifiers := map[int][]nodes.Node{}
		removedIdentifiers := map[int][]nodes.Node{}
		for _, n := range nodesAdded {
			pos := uast.PositionsOf(n.(nodes.Object))
			if pos.Start() != nil {
				line := int(pos.Start().Line)
				if focusedLinesAfter[line] {
					addedIdentifiers[line] = append(addedIdentifiers[line], n)
				}
			}
		}
		for _, n := range nodesRemoved {
			pos := uast.PositionsOf(n.(nodes.Object))
			line := int(pos.Start().Line)
			if pos.Start() != nil {
				if focusedLinesBefore[line] {
					removedIdentifiers[line] = append(removedIdentifiers[line], n)
				}
			}
		}
		for _, c := range candidates {
			nodesBefore := addedIdentifiers[c.Before]
			nodesAfter := removedIdentifiers[c.After]
			if len(nodesBefore) == 1 && len(nodesAfter) == 1 {
				idBefore := uast.TokenOf(nodesBefore[0].(nodes.Object))
				idAfter := uast.TokenOf(nodesAfter[0].(nodes.Object))
				println("!!!!!!!!!!!!!!!!!!!!!!!!!!", idBefore, idAfter)
				tdb.typos = append(tdb.typos, Typo{
					Wrong:   idBefore,
					Correct: idAfter,
					Commit:  commit,
					File:    change.Change.To.Name,
					Line:    c.After,
				})
			}
		}
	}
	return nil, nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (tdb *TyposDatasetBuilder) Finalize() interface{} {
	// deduplicate
	typos := make([]Typo, 0, len(tdb.typos))
	pairs := map[string]bool{}
	for _, t := range tdb.typos {
		id := t.Wrong + "|" + t.Correct
		if _, exists := pairs[id]; !exists {
			pairs[id] = true
			typos = append(typos, t)
		}
	}
	return TyposResult{Typos: typos}
}

// Fork clones this pipeline item.
func (tdb *TyposDatasetBuilder) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(tdb, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (tdb *TyposDatasetBuilder) Serialize(result interface{}, binary bool, writer io.Writer) error {
	commitsResult := result.(TyposResult)
	if binary {
		return tdb.serializeBinary(&commitsResult, writer)
	}
	tdb.serializeText(&commitsResult, writer)
	return nil
}

func (tdb *TyposDatasetBuilder) serializeText(result *TyposResult, writer io.Writer) {
	for _, t := range result.Typos {
		fmt.Fprintf(writer, "  - wrong: %s\n", t.Wrong)
		fmt.Fprintf(writer, "    correct: %d\n", t.Correct)
		fmt.Fprintf(writer, "    commit: %d\n", t.Commit.String())
		fmt.Fprintf(writer, "    file: %s\n", t.File)
		fmt.Fprintf(writer, "    line: %s\n", t.Line)
	}
}

func (tdb *TyposDatasetBuilder) serializeBinary(result *TyposResult, writer io.Writer) error {
	message := pb.TyposDataset{}
	message.Typos = make([]*pb.Typo, len(result.Typos))
	for i, t := range result.Typos {
		message.Typos[i] = &pb.Typo{
			Wrong:   t.Wrong,
			Correct: t.Correct,
			Commit:  t.Commit.String(),
			File:    t.File,
			Line:    int32(t.Line),
		}
	}
	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

func init() {
	core.Registry.Register(&TyposDatasetBuilder{})
}

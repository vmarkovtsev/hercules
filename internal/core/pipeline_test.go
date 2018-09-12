package core

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/hercules.v4/internal/pb"
	"gopkg.in/src-d/hercules.v4/internal/test"
)

type testPipelineItem struct {
	Initialized   bool
	DepsConsumed  bool
	Forked        bool
	Merged        *bool
	CommitMatches bool
	IndexMatches  bool
	MergeState    *int
	TestError     bool
}

func (item *testPipelineItem) Name() string {
	return "Test"
}

func (item *testPipelineItem) Provides() []string {
	arr := [...]string{"test"}
	return arr[:]
}

func (item *testPipelineItem) Requires() []string {
	return []string{}
}

func (item *testPipelineItem) Configure(facts map[string]interface{}) {
}

func (item *testPipelineItem) ListConfigurationOptions() []ConfigurationOption {
	options := [...]ConfigurationOption{{
		Name:        "TestOption",
		Description: "The option description.",
		Flag:        "test-option",
		Type:        IntConfigurationOption,
		Default:     10,
	}}
	return options[:]
}

func (item *testPipelineItem) Flag() string {
	return "mytest"
}

func (item *testPipelineItem) Description() string {
	return "description!"
}

func (item *testPipelineItem) Features() []string {
	f := [...]string{"power"}
	return f[:]
}

func (item *testPipelineItem) Initialize(repository *git.Repository) {
	item.Initialized = repository != nil
	item.Merged = new(bool)
	item.MergeState = new(int)
}

func (item *testPipelineItem) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if item.TestError {
		return nil, errors.New("error")
	}
	obj, exists := deps[DependencyCommit]
	item.DepsConsumed = exists
	if item.DepsConsumed {
		commit := obj.(*object.Commit)
		item.CommitMatches = commit.Hash == plumbing.NewHash(
			"af9ddc0db70f09f3f27b4b98e415592a7485171c")
		obj, item.DepsConsumed = deps[DependencyIndex]
		if item.DepsConsumed {
			item.IndexMatches = obj.(int) == 0
		}
	}
	obj, exists = deps[DependencyIsMerge]
	if exists {
		*item.MergeState++
		if obj.(bool) {
			*item.MergeState++
		}
	}
	return map[string]interface{}{"test": item}, nil
}

func (item *testPipelineItem) Fork(n int) []PipelineItem {
	result := make([]PipelineItem, n)
	for i := 0; i < n; i++ {
		result[i] = &testPipelineItem{Merged: item.Merged, MergeState: item.MergeState}
	}
	item.Forked = true
	return result
}

func (item *testPipelineItem) Merge(branches []PipelineItem) {
	*item.Merged = true
}

func (item *testPipelineItem) Finalize() interface{} {
	return item
}

func (item *testPipelineItem) Serialize(result interface{}, binary bool, writer io.Writer) error {
	return nil
}

type dependingTestPipelineItem struct {
	DependencySatisfied  bool
	TestNilConsumeReturn bool
}

func (item *dependingTestPipelineItem) Name() string {
	return "Test2"
}

func (item *dependingTestPipelineItem) Provides() []string {
	arr := [...]string{"test2"}
	return arr[:]
}

func (item *dependingTestPipelineItem) Requires() []string {
	arr := [...]string{"test"}
	return arr[:]
}

func (item *dependingTestPipelineItem) ListConfigurationOptions() []ConfigurationOption {
	options := [...]ConfigurationOption{{
		Name:        "TestOption2",
		Description: "The option description.",
		Flag:        "test-option2",
		Type:        IntConfigurationOption,
		Default:     10,
	}}
	return options[:]
}

func (item *dependingTestPipelineItem) Configure(facts map[string]interface{}) {
}

func (item *dependingTestPipelineItem) Initialize(repository *git.Repository) {
}

func (item *dependingTestPipelineItem) Flag() string {
	return "depflag"
}

func (item *dependingTestPipelineItem) Description() string {
	return "another description"
}

func (item *dependingTestPipelineItem) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	_, exists := deps["test"]
	item.DependencySatisfied = exists
	if !item.TestNilConsumeReturn {
		return map[string]interface{}{"test2": item}, nil
	}
	return nil, nil
}

func (item *dependingTestPipelineItem) Fork(n int) []PipelineItem {
	return make([]PipelineItem, n)
}

func (item *dependingTestPipelineItem) Merge(branches []PipelineItem) {
}

func (item *dependingTestPipelineItem) Finalize() interface{} {
	return true
}

func (item *dependingTestPipelineItem) Serialize(result interface{}, binary bool, writer io.Writer) error {
	return nil
}

func TestPipelineFacts(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	pipeline.SetFact("fact", "value")
	assert.Equal(t, pipeline.GetFact("fact"), "value")
}

func TestPipelineFeatures(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	pipeline.SetFeature("feat")
	val, _ := pipeline.GetFeature("feat")
	assert.True(t, val)
	_, exists := pipeline.GetFeature("!")
	assert.False(t, exists)
	Registry.featureFlags.Set("777")
	defer func() {
		Registry.featureFlags = arrayFeatureFlags{Flags: []string{}, Choices: map[string]bool{}}
	}()
	pipeline.SetFeaturesFromFlags()
	_, exists = pipeline.GetFeature("777")
	assert.False(t, exists)
	assert.Panics(t, func() {
		pipeline.SetFeaturesFromFlags(
			&PipelineItemRegistry{}, &PipelineItemRegistry{})
	})
}

func TestPipelineRun(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	pipeline.Initialize(map[string]interface{}{})
	assert.True(t, item.Initialized)
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.Repository.CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c"))
	result, err := pipeline.Run(commits)
	assert.Nil(t, err)
	assert.Equal(t, 2, len(result))
	assert.Equal(t, item, result[item].(*testPipelineItem))
	common := result[nil].(*CommonAnalysisResult)
	assert.Equal(t, common.BeginTime, int64(1481719198))
	assert.Equal(t, common.EndTime, int64(1481719198))
	assert.Equal(t, common.CommitsNumber, 1)
	assert.True(t, common.RunTime.Nanoseconds()/1e6 < 100)
	assert.Len(t, common.RunTimePerItem, 1)
	for key, val := range common.RunTimePerItem {
		assert.True(t, val >= 0, key)
	}
	assert.True(t, item.DepsConsumed)
	assert.True(t, item.CommitMatches)
	assert.True(t, item.IndexMatches)
	assert.Equal(t, 1, *item.MergeState)
	assert.True(t, item.Forked)
	assert.False(t, *item.Merged)
	pipeline.RemoveItem(item)
	result, err = pipeline.Run(commits)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(result))
}

func TestPipelineRunBranches(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	item := &testPipelineItem{}
	pipeline.AddItem(item)
	pipeline.Initialize(map[string]interface{}{})
	assert.True(t, item.Initialized)
	commits := make([]*object.Commit, 5)
	hashes := []string {
		"6db8065cdb9bb0758f36a7e75fc72ab95f9e8145",
		"f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee",
		"8a03b5620b1caa72ec9cb847ea88332621e2950a",
		"dd9dd084d5851d7dc4399fc7dbf3d8292831ebc5",
		"f4ed0405b14f006c0744029d87ddb3245607587a",
	}
	for i, h := range hashes {
		var err error
		commits[i], err = test.Repository.CommitObject(plumbing.NewHash(h))
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := pipeline.Run(commits)
	assert.Nil(t, err)
	assert.True(t, item.Forked)
	assert.True(t, *item.Merged)
	assert.Equal(t, 2, len(result))
	assert.Equal(t, item, result[item].(*testPipelineItem))
	common := result[nil].(*CommonAnalysisResult)
	assert.Equal(t, common.CommitsNumber, 5)
	assert.Equal(t, *item.MergeState, 8)
}

func TestPipelineOnProgress(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	progressOk := 0

	onProgress := func(step int, total int) {
		if step == 1 && total == 4 {
			progressOk++
		}
		if step == 2 && total == 4 {
			progressOk++
		}
		if step == 3 && total == 4 {
			progressOk++
		}
		if step == 4 && total == 4 {
			progressOk++
		}
	}

	pipeline.OnProgress = onProgress
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.Repository.CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c"))
	result, err := pipeline.Run(commits)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, 4, progressOk)
}

func TestPipelineCommitsFull(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	commits, err := pipeline.Commits(false)
	assert.Nil(t, err)
	assert.True(t, len(commits) >= 100)
	hashMap := map[plumbing.Hash]bool{}
	for _, c := range commits {
		hashMap[c.Hash] = true
	}
	assert.Equal(t, len(commits), len(hashMap))
	assert.Contains(t, hashMap, plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1"))
	assert.Contains(t, hashMap, plumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3"))
}

func TestPipelineCommitsFirstParent(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	commits, err := pipeline.Commits(true)
	assert.Nil(t, err)
	assert.True(t, len(commits) >= 100)
	hashMap := map[plumbing.Hash]bool{}
	for _, c := range commits {
		hashMap[c.Hash] = true
	}
	assert.Equal(t, len(commits), len(hashMap))
	assert.Contains(t, hashMap, plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1"))
	assert.NotContains(t, hashMap, plumbing.NewHash(
		"a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3"))
}

func TestLoadCommitsFromFile(t *testing.T) {
	tmp, err := ioutil.TempFile("", "hercules-test-")
	assert.Nil(t, err)
	tmp.WriteString("cce947b98a050c6d356bc6ba95030254914027b1\n6db8065cdb9bb0758f36a7e75fc72ab95f9e8145")
	tmp.Close()
	defer os.Remove(tmp.Name())
	commits, err := LoadCommitsFromFile(tmp.Name(), test.Repository)
	assert.Nil(t, err)
	assert.Equal(t, len(commits), 2)
	assert.Equal(t, commits[0].Hash, plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1"))
	assert.Equal(t, commits[1].Hash, plumbing.NewHash(
		"6db8065cdb9bb0758f36a7e75fc72ab95f9e8145"))
	commits, err = LoadCommitsFromFile("/WAT?xxx!", test.Repository)
	assert.Nil(t, commits)
	assert.NotNil(t, err)
	tmp, err = ioutil.TempFile("", "hercules-test-")
	assert.Nil(t, err)
	tmp.WriteString("WAT")
	tmp.Close()
	defer os.Remove(tmp.Name())
	commits, err = LoadCommitsFromFile(tmp.Name(), test.Repository)
	assert.Nil(t, commits)
	assert.NotNil(t, err)
	tmp, err = ioutil.TempFile("", "hercules-test-")
	assert.Nil(t, err)
	tmp.WriteString("ffffffffffffffffffffffffffffffffffffffff")
	tmp.Close()
	defer os.Remove(tmp.Name())
	commits, err = LoadCommitsFromFile(tmp.Name(), test.Repository)
	assert.Nil(t, commits)
	assert.NotNil(t, err)
}

func TestPipelineDeps(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	item1 := &dependingTestPipelineItem{}
	item2 := &testPipelineItem{}
	pipeline.AddItem(item1)
	pipeline.AddItem(item2)
	assert.Equal(t, pipeline.Len(), 2)
	pipeline.Initialize(map[string]interface{}{})
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.Repository.CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c"))
	result, err := pipeline.Run(commits)
	assert.Nil(t, err)
	assert.True(t, result[item1].(bool))
	assert.Equal(t, result[item2], item2)
	item1.TestNilConsumeReturn = true
	assert.Panics(t, func() { pipeline.Run(commits) })
}

func TestPipelineDeployFeatures(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	pipeline.DeployItem(&testPipelineItem{})
	f, _ := pipeline.GetFeature("power")
	assert.True(t, f)
}

func TestPipelineError(t *testing.T) {
	pipeline := NewPipeline(test.Repository)
	item := &testPipelineItem{}
	item.TestError = true
	pipeline.AddItem(item)
	pipeline.Initialize(map[string]interface{}{})
	commits := make([]*object.Commit, 1)
	commits[0], _ = test.Repository.CommitObject(plumbing.NewHash(
		"af9ddc0db70f09f3f27b4b98e415592a7485171c"))
	result, err := pipeline.Run(commits)
	assert.Nil(t, result)
	assert.NotNil(t, err)
}

func TestCommonAnalysisResultMerge(t *testing.T) {
	c1 := CommonAnalysisResult{
		BeginTime: 1513620635, EndTime: 1513720635, CommitsNumber: 1, RunTime: 100,
	    RunTimePerItem: map[string]float64{"one": 1, "two": 2}}
	assert.Equal(t, c1.BeginTimeAsTime().Unix(), int64(1513620635))
	assert.Equal(t, c1.EndTimeAsTime().Unix(), int64(1513720635))
	c2 := CommonAnalysisResult{
		BeginTime: 1513620535, EndTime: 1513730635, CommitsNumber: 2, RunTime: 200,
		RunTimePerItem: map[string]float64{"two": 4, "three": 8}}
	c1.Merge(&c2)
	assert.Equal(t, c1.BeginTime, int64(1513620535))
	assert.Equal(t, c1.EndTime, int64(1513730635))
	assert.Equal(t, c1.CommitsNumber, 3)
	assert.Equal(t, c1.RunTime.Nanoseconds(), int64(300))
	assert.Equal(t, c1.RunTimePerItem, map[string]float64{"one": 1, "two": 6, "three": 8})
}

func TestCommonAnalysisResultMetadata(t *testing.T) {
	c1 := &CommonAnalysisResult{
		BeginTime: 1513620635, EndTime: 1513720635, CommitsNumber: 1, RunTime: 100 * 1e6,
		RunTimePerItem: map[string]float64{"one": 1, "two": 2}}
	meta := &pb.Metadata{}
	c1 = MetadataToCommonAnalysisResult(c1.FillMetadata(meta))
	assert.Equal(t, c1.BeginTimeAsTime().Unix(), int64(1513620635))
	assert.Equal(t, c1.EndTimeAsTime().Unix(), int64(1513720635))
	assert.Equal(t, c1.CommitsNumber, 1)
	assert.Equal(t, c1.RunTime.Nanoseconds(), int64(100*1e6))
	assert.Equal(t, c1.RunTimePerItem, map[string]float64{"one": 1, "two": 2})
}

func TestConfigurationOptionTypeString(t *testing.T) {
	opt := ConfigurationOptionType(0)
	assert.Equal(t, opt.String(), "")
	opt = ConfigurationOptionType(1)
	assert.Equal(t, opt.String(), "int")
	opt = ConfigurationOptionType(2)
	assert.Equal(t, opt.String(), "string")
	opt = ConfigurationOptionType(3)
	assert.Equal(t, opt.String(), "float")
	opt = ConfigurationOptionType(4)
	assert.Equal(t, opt.String(), "string")
	opt = ConfigurationOptionType(5)
	assert.Panics(t, func() { _ = opt.String() })
}

func TestConfigurationOptionFormatDefault(t *testing.T) {
	opt := ConfigurationOption{Type: StringConfigurationOption, Default: "ololo"}
	assert.Equal(t, opt.FormatDefault(), "\"ololo\"")
	opt = ConfigurationOption{Type: IntConfigurationOption, Default: 7}
	assert.Equal(t, opt.FormatDefault(), "7")
	opt = ConfigurationOption{Type: BoolConfigurationOption, Default: false}
	assert.Equal(t, opt.FormatDefault(), "false")
	opt = ConfigurationOption{Type: FloatConfigurationOption, Default: 0.5}
	assert.Equal(t, opt.FormatDefault(), "0.5")
}

func TestPrepareRunPlanTiny(t *testing.T) {
	rootCommit, err := test.Repository.CommitObject(plumbing.NewHash(
		"cce947b98a050c6d356bc6ba95030254914027b1"))
	if err != nil {
		t.Fatal(err)
	}
	plan := prepareRunPlan([]*object.Commit{rootCommit})
	assert.Len(t, plan, 2)
	assert.Equal(t, runActionEmerge, plan[0].Action)
	assert.Equal(t, rootBranchIndex, plan[0].Items[0])
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[0].Commit.Hash.String())
	assert.Equal(t, runActionCommit, plan[1].Action)
	assert.Equal(t, rootBranchIndex, plan[1].Items[0])
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[1].Commit.Hash.String())
}

func TestPrepareRunPlanSmall(t *testing.T) {
	cit, err := test.Repository.Log(&git.LogOptions{From: plumbing.ZeroHash})
	if err != nil {
		panic(err)
	}
	defer cit.Close()
	var commits []*object.Commit
	timeCutoff := time.Date(2016, 12, 15, 0, 0, 0, 0, time.FixedZone("CET", 7200))
	cit.ForEach(func(commit *object.Commit) error {
		reliableTime := time.Date(commit.Author.When.Year(), commit.Author.When.Month(),
			commit.Author.When.Day(), commit.Author.When.Hour(), commit.Author.When.Minute(),
			commit.Author.When.Second(), 0, time.FixedZone("CET", 7200))
		if reliableTime.Before(timeCutoff) {
			commits = append(commits, commit)
		}
		return nil
	})
	plan := prepareRunPlan(commits)
	/*for _, p := range plan {
		if p.Commit != nil {
			fmt.Println(p.Action, p.Commit.Hash.String(), p.Items)
		} else {
			fmt.Println(p.Action, strings.Repeat(" ", 40), p.Items)
		}
	}*/
	// fork, merge and one artificial commit per branch
	assert.Len(t, plan, len(commits) + 1)
	assert.Equal(t, runActionEmerge, plan[0].Action)
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[0].Commit.Hash.String())
	assert.Equal(t, rootBranchIndex, plan[0].Items[0])
	assert.Equal(t, runActionCommit, plan[1].Action)
	assert.Equal(t, rootBranchIndex, plan[1].Items[0])
	assert.Equal(t, "cce947b98a050c6d356bc6ba95030254914027b1", plan[1].Commit.Hash.String())
	assert.Equal(t, runActionCommit, plan[2].Action)
	assert.Equal(t, rootBranchIndex, plan[2].Items[0])
	assert.Equal(t, "a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3", plan[2].Commit.Hash.String())
	assert.Equal(t, runActionCommit, plan[10].Action)
	assert.Equal(t, rootBranchIndex, plan[10].Items[0])
	assert.Equal(t, "a28e9064c70618dc9d68e1401b889975e0680d11", plan[10].Commit.Hash.String())
}

func TestMergeDag(t *testing.T) {
	cit, err := test.Repository.Log(&git.LogOptions{From: plumbing.ZeroHash})
	if err != nil {
		panic(err)
	}
	defer cit.Close()
	var commits []*object.Commit
	timeCutoff := time.Date(2017, 8, 12, 0, 0, 0, 0, time.FixedZone("CET", 7200))
	cit.ForEach(func(commit *object.Commit) error {
		reliableTime := time.Date(commit.Author.When.Year(), commit.Author.When.Month(),
			commit.Author.When.Day(), commit.Author.When.Hour(), commit.Author.When.Minute(),
			commit.Author.When.Second(), 0, time.FixedZone("CET", 7200))
		if reliableTime.Before(timeCutoff) {
			commits = append(commits, commit)
		}
		return nil
	})
	hashes, dag := buildDag(commits)
	leaveRootComponent(hashes, dag)
	mergedDag, _ := mergeDag(hashes, dag)
	for key, vals := range mergedDag {
		if key != plumbing.NewHash("a28e9064c70618dc9d68e1401b889975e0680d11") &&
			key != plumbing.NewHash("db325a212d0bc99b470e000641d814745024bbd5") {
			assert.Len(t, vals, len(dag[key]), key.String())
		} else {
			mvals := map[string]bool{}
			for _, val := range vals {
				mvals[val.Hash.String()] = true
			}
			if key == plumbing.NewHash("a28e9064c70618dc9d68e1401b889975e0680d11") {
				assert.Contains(t, mvals, "db325a212d0bc99b470e000641d814745024bbd5")
				assert.Contains(t, mvals, "be9b61e09b08b98e64ed461a4004c9e2412f78ee")
			}
			if key == plumbing.NewHash("db325a212d0bc99b470e000641d814745024bbd5") {
				assert.Contains(t, mvals, "f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee")
				assert.Contains(t, mvals, "8a03b5620b1caa72ec9cb847ea88332621e2950a")
			}
		}
	}
	assert.Len(t, mergedDag, 8)
	assert.Contains(t, mergedDag, plumbing.NewHash("cce947b98a050c6d356bc6ba95030254914027b1"))
	assert.Contains(t, mergedDag, plumbing.NewHash("a3ee37f91f0d705ec9c41ae88426f0ae44b2fbc3"))
	assert.Contains(t, mergedDag, plumbing.NewHash("a28e9064c70618dc9d68e1401b889975e0680d11"))
	assert.Contains(t, mergedDag, plumbing.NewHash("be9b61e09b08b98e64ed461a4004c9e2412f78ee"))
	assert.Contains(t, mergedDag, plumbing.NewHash("db325a212d0bc99b470e000641d814745024bbd5"))
	assert.Contains(t, mergedDag, plumbing.NewHash("f30daba81ff2bf0b3ba02a1e1441e74f8a4f6fee"))
	assert.Contains(t, mergedDag, plumbing.NewHash("8a03b5620b1caa72ec9cb847ea88332621e2950a"))
	assert.Contains(t, mergedDag, plumbing.NewHash("dd9dd084d5851d7dc4399fc7dbf3d8292831ebc5"))
	queue := []plumbing.Hash{plumbing.NewHash("cce947b98a050c6d356bc6ba95030254914027b1")}
	visited := map[plumbing.Hash]bool{}
	for len(queue) > 0 {
		head := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if visited[head] {
			continue
		}
		visited[head] = true
		for _, child := range mergedDag[head] {
			queue = append(queue, child.Hash)
		}
	}
	assert.Len(t, visited, 8)
}

func TestPrepareRunPlanBig(t *testing.T) {
	cases := [][7]int {
		{2017, 8, 9, 0, 0, 0, 0},
		{2017, 8, 10, 0, 0, 0, 0},
		{2017, 8, 24, 1, 1, 1, 1},
		{2017, 9, 19, 1-2, 1, 1, 1},
		{2017, 9, 23, 1-2, 1, 1, 1},
		{2017, 12, 8, 1, 1, 1, 1},
		{2017, 12, 9, 1, 1, 1, 1},
		{2017, 12, 10, 1, 1, 1, 1},
		{2017, 12, 11, 2, 2, 2, 2},
		{2017, 12, 19, 3, 3, 3, 3},
		{2017, 12, 27, 3, 3, 3, 3},
		{2018, 1, 10, 3, 3, 3, 3},
		{2018, 1, 16, 3, 3, 3, 3},
		{2018, 1, 18, 4, 5, 4, 4},
		{2018, 1, 23, 5, 5, 5, 5},
		{2018, 3, 12, 6, 6, 6, 6},
		{2018, 5, 13, 6, 6, 6, 6},
		{2018, 5, 16, 7, 7, 7, 7},
	}
	for _, testCase := range cases {
		func() {
			cit, err := test.Repository.Log(&git.LogOptions{From: plumbing.ZeroHash})
			if err != nil {
				panic(err)
			}
			defer cit.Close()
			var commits []*object.Commit
			timeCutoff := time.Date(
				testCase[0], time.Month(testCase[1]), testCase[2], 0, 0, 0, 0, time.FixedZone("CET", 7200))
			cit.ForEach(func(commit *object.Commit) error {
				reliableTime := time.Date(commit.Author.When.Year(), commit.Author.When.Month(),
					commit.Author.When.Day(), commit.Author.When.Hour(), commit.Author.When.Minute(),
					commit.Author.When.Second(), 0, time.FixedZone("CET", 7200))
				if reliableTime.Before(timeCutoff) {
					commits = append(commits, commit)
				}
				return nil
			})
			plan := prepareRunPlan(commits)
			/*for _, p := range plan {
				if p.Commit != nil {
					fmt.Println(p.Action, p.Commit.Hash.String(), p.Items)
				} else {
					fmt.Println(p.Action, strings.Repeat(" ", 40), p.Items)
				}
			}*/
			numCommits := 0
			numForks := 0
			numMerges := 0
			numDeletes := 0
			numEmerges := 0
			processed := map[plumbing.Hash]map[int]int{}
			for _, p := range plan {
				switch p.Action {
				case runActionCommit:
					branches := processed[p.Commit.Hash]
					if branches == nil {
						branches = map[int]int{}
						processed[p.Commit.Hash] = branches
					}
					branches[p.Items[0]]++
					for _, parent := range p.Commit.ParentHashes {
						assert.Contains(t, processed, parent)
					}
					numCommits++
				case runActionFork:
					numForks++
				case runActionMerge:
					counts := map[int]int{}
					for _, i := range p.Items {
						counts[i]++
					}
					for x, v := range counts {
						assert.Equal(t, 1, v, x)
					}
					numMerges++
				case runActionDelete:
					numDeletes++
				case runActionEmerge:
					numEmerges++
				}
			}
			for c, branches := range processed {
				for b, v := range branches {
					assert.Equal(t, 1, v, fmt.Sprint(c.String(), b))
				}
			}
			assert.Equal(t, numCommits, len(commits)+testCase[3], fmt.Sprintf("commits %v", testCase))
			assert.Equal(t, numForks, testCase[4], fmt.Sprintf("forks %v", testCase))
			assert.Equal(t, numMerges, testCase[5], fmt.Sprintf("merges %v", testCase))
			assert.Equal(t, numDeletes, testCase[6], fmt.Sprintf("deletes %v", testCase))
			assert.Equal(t, numEmerges, 1, fmt.Sprintf("emerges %v", testCase))
		}()
	}
}

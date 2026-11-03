package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Schedules: what a project's Work does by itself, on time, while Gerdr's backend is up (the background service keeps
// it up with the App closed). Declared in the Work directory, one file per job, the file name is its id:
//
//	schedules/mrwatch.json   { "fn": "mrwatch.tick", "every": "5m", "name": "Watch my MRs" }
//	schedules/standup.json   { "prompt": "Sum up yesterday's MRs for the standup", "at": "09:30", "name": "Standup" }
//
// fn runs a local function (deterministic work: poll, filter, hand only what needs judgement to an agent with
// ctx.sessions.send); prompt hands the text to this project's Work agent, as if the user had sent it. Exactly one of
// the two. every is an interval (Go durations or days: 30s, 30m, 6h, 1d; at least 10s); at is times of day, local time,
// comma separated ("09:00,16:00"); at wins when both are there. input is the fn's input ({ scheduled: true } is added).
//
// Kept compatible with a hosted function platform's schedules so a schedule written for one reads the same on the
// other; left out for now: jitter, due (rows of a table), count, params and the user's overrides.
//
// Missed runs (the computer asleep, Gerdr not running) are caught up once, not once per miss: for every, the next run
// is last + every, so a late one runs at once; for at, the latest time passed today that hasn't run. A job still
// running when its time comes again is skipped. The last run and the paused flag live outside the Work directory
// (<GERDR_DIR>/state/work-schedules/<project>.json): they're this machine's, not the project's.

// SchedulesDir is the Work directory's folder of schedule files.
const SchedulesDir = "schedules"

// minEvery: seconds are allowed (a watcher the user wants tight), but not a busy loop over their CLIs and APIs.
// Annulo's floor is 5m; Gerdr's watchers poll one person's MRs, where 10s is still cheap.
const minEvery = 10 * time.Second

// scheduleTick: how often due schedules are looked for; small enough for the shortest every to keep its time.
const scheduleTick = 5 * time.Second

var (
	scheduleIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	atRe         = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)
)

// Schedule is one schedules/<id>.json.
type Schedule struct {
	ID     string `json:"id"`
	Fn     string `json:"fn,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	Name   string `json:"name,omitempty"`
	Every  string `json:"every,omitempty"`
	At     string `json:"at,omitempty"`
	Input  any    `json:"input,omitempty"`

	every time.Duration
	ats   []int // minutes into the day, ascending
}

// ScheduleRun is how a run went.
type ScheduleRun struct {
	StartedAt time.Time `json:"startedAt"`
	Ms        int64     `json:"ms"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	Result    string    `json:"result,omitempty"` // the fn's return value as JSON, cut to 500 characters
	RunID     string    `json:"runId,omitempty"`  // fn: the run's id (gerdr work logs --id)
}

type scheduleState struct {
	Paused bool         `json:"paused,omitempty"`
	Last   *ScheduleRun `json:"last,omitempty"`
	Fails  int          `json:"fails,omitempty"` // failures in a row
}

// ScheduleInfo is a schedule with how it's doing, for `gerdr work schedules` and pages.
type ScheduleInfo struct {
	Schedule
	Paused  bool         `json:"paused,omitempty"`
	Running bool         `json:"running,omitempty"`
	Last    *ScheduleRun `json:"last,omitempty"`
	Fails   int          `json:"fails,omitempty"`
	Next    *time.Time   `json:"next,omitempty"`
}

// ParseSchedules reads a Work directory's schedules/; a broken file is an error naming it (none run until it's fixed:
// half a set of watchers running silently is worse than a loud stop).
func ParseSchedules(dir string) ([]Schedule, error) {
	ents, err := os.ReadDir(filepath.Join(dir, SchedulesDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Schedule
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !ok {
			continue
		}
		rel := SchedulesDir + "/" + e.Name()
		if !scheduleIDRe.MatchString(id) {
			return nil, fmt.Errorf(i18n.S("%s：文件名只能用字母、数字、. - _", "%s: the file name may only use letters, digits, . - _"), rel)
		}
		b, err := os.ReadFile(filepath.Join(dir, SchedulesDir, e.Name()))
		if err != nil {
			return nil, err
		}
		var s Schedule
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf(i18n.S("%s 格式不对：%w", "%s is malformed: %w"), rel, err)
		}
		s.ID, s.Prompt = id, strings.TrimSpace(s.Prompt)
		if err := s.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Schedule) check() error {
	switch {
	case (s.Fn != "") == (s.Prompt != ""):
		return errors.New(i18n.S("fn 和 prompt 要写一个，只写一个", "needs exactly one of fn and prompt"))
	case s.Fn != "":
		file, fn, ok := strings.Cut(s.Fn, ".")
		if !ok || fn == "" || !fileRE.MatchString(file) {
			return errors.New(i18n.S("fn 要写成 文件.函数（比如 mrwatch.tick）", "fn must be file.function (e.g. mrwatch.tick)"))
		}
	}
	if strings.TrimSpace(s.At) != "" {
		ats, err := parseAts(s.At)
		if err != nil {
			return err
		}
		s.ats, s.Every = ats, ""
		return nil
	}
	if s.Every == "" {
		return errors.New(i18n.S(`要写 every（比如 "30s"、"5m"、"6h"、"1d"）或 at（比如 "09:00"）`, `needs every (e.g. "30s", "5m", "6h", "1d") or at (e.g. "09:00")`))
	}
	d, err := parseSpan(s.Every)
	if err != nil {
		return err
	}
	if d < minEvery {
		return fmt.Errorf(i18n.S("every 最短 %s", "every is at least %s"), minEvery)
	}
	s.every = d
	return nil
}

func parseAts(v string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(v, ",") {
		m := atRe.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil {
			return nil, errors.New(i18n.S("at 要写成 HH:MM（比如 09:00），几个时间用逗号隔开", "at must be HH:MM (e.g. 09:00), several separated by commas"))
		}
		var h, mi int
		fmt.Sscanf(m[1]+" "+m[2], "%d %d", &h, &mi)
		if !containsInt(out, h*60+mi) {
			out = append(out, h*60+mi)
		}
	}
	sort.Ints(out)
	return out, nil
}

func containsInt(l []int, v int) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// parseSpan reads Go durations (30m, 6h) and days (1d, 7d).
func parseSpan(v string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(n, "%d", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf(i18n.S("%q 不是时长（写成 30s、30m、6h、1d）", "%q isn't a duration (write 30s, 30m, 6h, 1d)"), v)
	}
	return d, nil
}

// next is when it should run next, given when it last started (zero: never).
func (s *Schedule) next(last, now time.Time) time.Time {
	if len(s.ats) > 0 {
		// Today's and tomorrow's times. Only today's missed one is caught up: opening the laptop in the morning
		// doesn't run yesterday's, today's comes soon enough.
		var passed, upcoming time.Time
		for _, day := range []int{0, 1} {
			d := now.AddDate(0, 0, day)
			for _, m := range s.ats {
				t := time.Date(d.Year(), d.Month(), d.Day(), m/60, m%60, 0, 0, now.Location())
				if !t.After(now) {
					passed = t
				} else if upcoming.IsZero() {
					upcoming = t
				}
			}
		}
		if !passed.IsZero() && last.Before(passed) {
			return passed
		}
		return upcoming
	}
	if last.IsZero() {
		return now
	}
	return last.Add(s.every)
}

// schedProject is one project's schedules: definitions (re-read when schedules/ changes), state, what's running.
type schedProject struct {
	sig     string
	defs    []Schedule
	err     error
	state   map[string]*scheduleState
	running map[string]bool
	file    string
}

type scheduler struct {
	mu       sync.Mutex
	projects map[string]*schedProject
}

func (r *Runner) stateFile(project string) string {
	return filepath.Join(r.Root, "state", "work-schedules", project+".json")
}

// sched is the project's schedules, re-read when the folder changed. Caller holds r.sched.mu.
func (r *Runner) sched(project string) *schedProject {
	if project == "" {
		project = DefaultProject // "" and "default" are one project: one state, one running set
	}
	if r.sched_.projects == nil {
		r.sched_.projects = map[string]*schedProject{}
	}
	p := r.sched_.projects[project]
	if p == nil {
		p = &schedProject{state: map[string]*scheduleState{}, running: map[string]bool{}, file: r.stateFile(project)}
		if b, err := os.ReadFile(p.file); err == nil {
			_ = json.Unmarshal(b, &p.state)
		}
		r.sched_.projects[project] = p
	}
	dir, err := r.Dir(project)
	if err != nil {
		p.defs, p.err = nil, err
		return p
	}
	if sig := dirSig(filepath.Join(dir, SchedulesDir)); sig != p.sig || sig == "" {
		p.sig = sig
		prev := p.err
		p.defs, p.err = ParseSchedules(dir)
		if p.err != nil && (prev == nil || prev.Error() != p.err.Error()) && r.Log != nil {
			r.Log.Warn("work: schedules not run", "project", project, "err", p.err)
		}
	}
	return p
}

// dirSig fingerprints a folder's files (names, sizes, times) so it's re-read only when something changed.
func dirSig(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range ents {
		if st, err := e.Info(); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", e.Name(), st.Size(), st.ModTime().UnixNano())
		}
	}
	return b.String()
}

func (p *schedProject) save() {
	_ = os.MkdirAll(filepath.Dir(p.file), 0o700)
	b, _ := json.MarshalIndent(p.state, "", "  ")
	_ = os.WriteFile(p.file, b, 0o600)
}

func (p *schedProject) st(id string) *scheduleState {
	s := p.state[id]
	if s == nil {
		s = &scheduleState{}
		p.state[id] = s
	}
	return s
}

// workProjects: the projects with a Work directory on disk (the scheduler creates none).
func (r *Runner) workProjects() []string {
	var out []string
	if st, err := os.Stat(filepath.Join(r.Root, "work")); err == nil && st.IsDir() {
		out = append(out, DefaultProject)
	}
	ents, _ := os.ReadDir(filepath.Join(r.Root, "projects"))
	for _, e := range ents {
		if e.IsDir() && validProject(e.Name()) {
			if st, err := os.Stat(filepath.Join(r.Root, "projects", e.Name(), "work")); err == nil && st.IsDir() {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

// RunSchedules runs every project's schedules until ctx ends.
func (r *Runner) RunSchedules(ctx context.Context) {
	go func() {
		// A moment after start: the hub is still loading sessions, MCP servers connecting
		select {
		case <-time.After(20 * time.Second):
		case <-ctx.Done():
			return
		}
		t := time.NewTicker(scheduleTick)
		defer t.Stop()
		for {
			r.tickSchedules(time.Now())
			select {
			case <-t.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (r *Runner) tickSchedules(now time.Time) {
	type job struct {
		project string
		s       Schedule
	}
	var due []job
	r.sched_.mu.Lock()
	for _, project := range r.workProjects() {
		p := r.sched(project)
		for _, s := range p.defs {
			st := p.state[s.ID]
			if p.running[s.ID] || st != nil && st.Paused {
				continue
			}
			var last time.Time
			if st != nil && st.Last != nil {
				last = st.Last.StartedAt
			}
			if !s.next(last, now).After(now) {
				due = append(due, job{project, s})
			}
		}
	}
	r.sched_.mu.Unlock()
	for _, j := range due {
		r.startScheduled(j.project, j.s)
	}
}

// startScheduled runs one schedule in the background and records how it went; false when it's already running.
func (r *Runner) startScheduled(project string, s Schedule) bool {
	r.sched_.mu.Lock()
	p := r.sched(project)
	if p.running[s.ID] {
		r.sched_.mu.Unlock()
		return false
	}
	p.running[s.ID] = true
	r.sched_.mu.Unlock()
	start := time.Now()
	go func() {
		run := &ScheduleRun{StartedAt: start}
		var err error
		if s.Fn != "" {
			input := map[string]any{}
			if m, ok := s.Input.(map[string]any); ok {
				for k, v := range m {
					input[k] = v
				}
			}
			input["scheduled"] = true
			rn := r.Start(project, s.Fn, input)
			run.RunID = rn.ID
			var res any
			if res, err = rn.Wait(context.Background()); err == nil {
				b, _ := json.Marshal(res)
				run.Result = cut(string(b), 500)
			}
		} else {
			err = r.schedulePrompt(project, s)
		}
		run.Ms, run.OK = time.Since(start).Milliseconds(), err == nil
		if err != nil {
			run.Error = err.Error()
			if r.Log != nil {
				r.Log.Warn("work: schedule failed", "project", project, "id", s.ID, "ms", run.Ms, "err", err)
			}
		}
		r.sched_.mu.Lock()
		defer r.sched_.mu.Unlock()
		delete(p.running, s.ID)
		st := p.st(s.ID)
		st.Last = run
		if run.OK {
			st.Fails = 0
		} else {
			st.Fails++
		}
		p.save()
	}()
	return true
}

// schedulePrompt hands a prompt schedule to the project's Work agent (queued if it's busy, like a report).
func (r *Runner) schedulePrompt(project string, s Schedule) error {
	if r.Host == nil {
		return errNoHost
	}
	name := s.Name
	if name == "" {
		name = s.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := r.Host.Send(ctx, project, "work", fmt.Sprintf(i18n.S("[定时任务「%s」]\n\n%s", "[Scheduled: %s]\n\n%s"), name, s.Prompt), nil)
	return err
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Schedules lists a project's schedules with their state and next time.
func (r *Runner) Schedules(project string) ([]ScheduleInfo, error) {
	r.sched_.mu.Lock()
	defer r.sched_.mu.Unlock()
	p := r.sched(project)
	if p.err != nil {
		return nil, p.err
	}
	now := time.Now()
	out := []ScheduleInfo{}
	for _, s := range p.defs {
		info := ScheduleInfo{Schedule: s, Running: p.running[s.ID]}
		var last time.Time
		if st := p.state[s.ID]; st != nil {
			info.Paused, info.Last, info.Fails = st.Paused, st.Last, st.Fails
			if st.Last != nil {
				last = st.Last.StartedAt
			}
		}
		if !info.Paused {
			n := s.next(last, now)
			if n.Before(now) {
				n = now
			}
			info.Next = &n
		}
		out = append(out, info)
	}
	return out, nil
}

// RunSchedule runs one now (the user's "run now"), whatever its time.
func (r *Runner) RunSchedule(project, id string) error {
	s, err := r.findSchedule(project, id)
	if err != nil {
		return err
	}
	if !r.startScheduled(project, s) {
		return errors.New(i18n.S("它正在跑", "it's running already"))
	}
	return nil
}

// PauseSchedule pauses (or resumes) one; the state is this machine's.
func (r *Runner) PauseSchedule(project, id string, paused bool) error {
	if _, err := r.findSchedule(project, id); err != nil {
		return err
	}
	r.sched_.mu.Lock()
	defer r.sched_.mu.Unlock()
	p := r.sched(project)
	p.st(id).Paused = paused
	p.save()
	return nil
}

func (r *Runner) findSchedule(project, id string) (Schedule, error) {
	r.sched_.mu.Lock()
	defer r.sched_.mu.Unlock()
	p := r.sched(project)
	if p.err != nil {
		return Schedule{}, p.err
	}
	for _, s := range p.defs {
		if s.ID == id {
			return s, nil
		}
	}
	return Schedule{}, fmt.Errorf(i18n.S("没有定时任务 %s（%s/%s.json）", "no schedule %s (%s/%s.json)"), id, SchedulesDir, id)
}

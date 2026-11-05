package mockstrava

import (
	"strconv"
	"time"
)

// The ride every activity is modelled on: 60 samples at 1 Hz, two laps,
// gear b1, the numbers of the worker's parser fixture.
const (
	sampleCount = 60
	lapCount    = 2
	gearBikeID  = "b1"
	gearShoeID  = "g1"
	segmentID   = int64(7)
	// A short valid encoded polyline (two points near the ride).
	segmentPolyline = "_p~iF~ps|U_ulLnnqC"
)

// activity is one stored ride; the JSON shape is Strava's DetailedActivity.
type activity struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	Type                 string  `json:"type"`
	SportType            string  `json:"sport_type"`
	StartDate            string  `json:"start_date"`
	StartDateLocal       string  `json:"start_date_local"`
	Timezone             string  `json:"timezone"`
	ElapsedTime          int     `json:"elapsed_time"`
	MovingTime           int     `json:"moving_time"`
	Distance             float64 `json:"distance"`
	TotalElevationGain   float64 `json:"total_elevation_gain"`
	AverageSpeed         float64 `json:"average_speed"`
	MaxSpeed             float64 `json:"max_speed"`
	AverageHeartrate     float64 `json:"average_heartrate"`
	MaxHeartrate         float64 `json:"max_heartrate"`
	HasHeartrate         bool    `json:"has_heartrate"`
	AverageWatts         float64 `json:"average_watts"`
	WeightedAverageWatts float64 `json:"weighted_average_watts"`
	MaxWatts             int     `json:"max_watts"`
	DeviceWatts          bool    `json:"device_watts"`
	AverageCadence       float64 `json:"average_cadence"`
	AverageTemp          float64 `json:"average_temp"`
	Calories             float64 `json:"calories"`
	Kilojoules           float64 `json:"kilojoules"`
	Commute              bool    `json:"commute"`
	Trainer              bool    `json:"trainer"`
	Manual               bool    `json:"manual"`
	Private              bool    `json:"private"`
	WorkoutType          *int    `json:"workout_type"`
	GearID               string  `json:"gear_id"`
	Gear                 *gear   `json:"gear"`
	Athlete              struct {
		ID int64 `json:"id"`
	} `json:"athlete"`
	Map            activityMap     `json:"map"`
	SegmentEfforts []segmentEffort `json:"segment_efforts"`
	Laps           []lap           `json:"laps"`
	Photos         photos          `json:"photos"`

	start   time.Time
	streams map[string]stream
}

// summary is the SummaryActivity of the list endpoint (no laps, efforts,
// description or photos).
type summary struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	SportType          string  `json:"sport_type"`
	StartDate          string  `json:"start_date"`
	StartDateLocal     string  `json:"start_date_local"`
	Timezone           string  `json:"timezone"`
	ElapsedTime        int     `json:"elapsed_time"`
	MovingTime         int     `json:"moving_time"`
	Distance           float64 `json:"distance"`
	TotalElevationGain float64 `json:"total_elevation_gain"`
	AverageSpeed       float64 `json:"average_speed"`
	HasHeartrate       bool    `json:"has_heartrate"`
	GearID             string  `json:"gear_id"`
	Athlete            struct {
		ID int64 `json:"id"`
	} `json:"athlete"`
}

func (a *activity) summary() summary {
	s := summary{
		ID: a.ID, Name: a.Name, Type: a.Type, SportType: a.SportType, StartDate: a.StartDate, StartDateLocal: a.StartDateLocal,
		Timezone: a.Timezone, ElapsedTime: a.ElapsedTime, MovingTime: a.MovingTime, Distance: a.Distance,
		TotalElevationGain: a.TotalElevationGain, AverageSpeed: a.AverageSpeed, HasHeartrate: a.HasHeartrate, GearID: a.GearID,
	}
	s.Athlete.ID = a.Athlete.ID
	return s
}

type activityMap struct {
	ID              string `json:"id"`
	Polyline        string `json:"polyline"`
	SummaryPolyline string `json:"summary_polyline"`
}

type photos struct {
	Primary *struct{} `json:"primary"`
	Count   int       `json:"count"`
}

// lap is one Strava Lap.
type lap struct {
	ID                 int64   `json:"id"`
	LapIndex           int     `json:"lap_index"`
	Name               string  `json:"name"`
	StartDate          string  `json:"start_date"`
	StartDateLocal     string  `json:"start_date_local"`
	ElapsedTime        int     `json:"elapsed_time"`
	MovingTime         int     `json:"moving_time"`
	Distance           float64 `json:"distance"`
	StartIndex         int     `json:"start_index"`
	EndIndex           int     `json:"end_index"`
	AverageSpeed       float64 `json:"average_speed"`
	MaxSpeed           float64 `json:"max_speed"`
	AverageHeartrate   float64 `json:"average_heartrate"`
	MaxHeartrate       float64 `json:"max_heartrate"`
	AverageWatts       float64 `json:"average_watts"`
	AverageCadence     float64 `json:"average_cadence"`
	TotalElevationGain float64 `json:"total_elevation_gain"`
}

// gear is a SummaryGear with the DetailedGear extras.
type gear struct {
	ID          string  `json:"id"`
	Primary     bool    `json:"primary"`
	Name        string  `json:"name"`
	Nickname    string  `json:"nickname"`
	Retired     bool    `json:"retired"`
	Distance    float64 `json:"distance"`
	BrandName   string  `json:"brand_name,omitempty"`
	ModelName   string  `json:"model_name,omitempty"`
	Description string  `json:"description,omitempty"`
}

var gearCatalog = map[string]gear{
	gearBikeID: {ID: gearBikeID, Primary: true, Name: "Canyon Grail", Nickname: "Canyon Grail", Distance: 1500.5,
		BrandName: "Canyon", ModelName: "Grail", Description: "gravel"},
	gearShoeID: {ID: gearShoeID, Name: "Pegasus", Nickname: "Pegasus", Distance: 320,
		BrandName: "Nike", ModelName: "Pegasus 41", Description: "road shoes"},
}

// summaryGear is the gear block embedded in an activity (no detail fields).
func summaryGear(id string) *gear {
	g := gearCatalog[id]
	return &gear{ID: g.ID, Primary: g.Primary, Name: g.Name, Nickname: g.Nickname, Retired: g.Retired, Distance: g.Distance}
}

// athleteGear is the gear part of GET /athlete.
type athleteGear struct {
	ID        int64  `json:"id"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
	Bikes     []gear `json:"bikes"`
	Shoes     []gear `json:"shoes"`
}

func (s *Server) athleteJSON() athleteGear {
	return athleteGear{ID: s.opts.AthleteID, Firstname: "E2E", Lastname: "Athlete",
		Bikes: []gear{*summaryGear(gearBikeID)}, Shoes: []gear{*summaryGear(gearShoeID)}}
}

// segmentEffort is a DetailedSegmentEffort with its SummarySegment.
type segmentEffort struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	ElapsedTime      int            `json:"elapsed_time"`
	MovingTime       int            `json:"moving_time"`
	StartDate        string         `json:"start_date"`
	StartDateLocal   string         `json:"start_date_local"`
	Distance         float64        `json:"distance"`
	StartIndex       int            `json:"start_index"`
	EndIndex         int            `json:"end_index"`
	AverageHeartrate float64        `json:"average_heartrate"`
	MaxHeartrate     float64        `json:"max_heartrate"`
	AverageWatts     float64        `json:"average_watts"`
	AverageCadence   float64        `json:"average_cadence"`
	Segment          summarySegment `json:"segment"`
}

type summarySegment struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	ActivityType  string  `json:"activity_type"`
	Distance      float64 `json:"distance"`
	AverageGrade  float64 `json:"average_grade"`
	MaximumGrade  float64 `json:"maximum_grade"`
	ClimbCategory int     `json:"climb_category"`
	Starred       bool    `json:"starred"`
}

// detailedSegment is GET /segments/{id}.
type detailedSegment struct {
	summarySegment
	TotalElevationGain float64 `json:"total_elevation_gain"`
	Map                struct {
		ID       string `json:"id"`
		Polyline string `json:"polyline"`
	} `json:"map"`
}

func segment7() summarySegment {
	return summarySegment{ID: segmentID, Name: "Isar Climb", ActivityType: "Ride", Distance: 240, AverageGrade: 1.5, MaximumGrade: 3, ClimbCategory: 0}
}

func detailedSegment7() detailedSegment {
	d := detailedSegment{summarySegment: segment7(), TotalElevationGain: 6}
	d.Map.ID = "s7"
	d.Map.Polyline = segmentPolyline
	return d
}

// stream is one channel of the key_by_type streams layout.
type stream struct {
	Type         string `json:"type"`
	Data         any    `json:"data"`
	SeriesType   string `json:"series_type"`
	OriginalSize int    `json:"original_size"`
	Resolution   string `json:"resolution"`
}

// Per-sample formulas of the ride; i is the second.
func sampleSpeed(i int) float64    { return 7.5 + 0.5*float64(i%4) }
func sampleHR(i int) int           { return 130 + i%20 }
func sampleWatts(i int) int        { return 180 + i }
func sampleCadence(i int) int      { return 85 + i%6 }
func sampleAltitude(i int) float64 { return 500 + 0.2*float64(i) }
func sampleGrade(i int) float64    { return float64(i%3) * 0.5 }

const sampleTemp = 21.0

func makeStreams() map[string]stream {
	n := sampleCount
	times, latlng := make([]int, n), make([][2]float64, n)
	alt, dist, speed, hr := make([]float64, n), make([]float64, n), make([]float64, n), make([]int, n)
	cad, watts, temp, grade := make([]int, n), make([]int, n), make([]float64, n), make([]float64, n)
	var d float64
	for i := range n {
		if i > 0 {
			d += sampleSpeed(i)
		}
		times[i], latlng[i] = i, [2]float64{48.1 + float64(i)*0.0001, 11.5 + float64(i)*0.0001}
		alt[i], dist[i], speed[i], hr[i] = sampleAltitude(i), round1(d), sampleSpeed(i), sampleHR(i)
		cad[i], watts[i], temp[i], grade[i] = sampleCadence(i), sampleWatts(i), sampleTemp, sampleGrade(i)
	}
	ch := func(typ string, data any) stream {
		return stream{Type: typ, Data: data, SeriesType: "distance", OriginalSize: n, Resolution: "high"}
	}
	return map[string]stream{
		"time": ch("time", times), "latlng": ch("latlng", latlng), "altitude": ch("altitude", alt),
		"distance": ch("distance", dist), "velocity_smooth": ch("velocity_smooth", speed), "heartrate": ch("heartrate", hr),
		"cadence": ch("cadence", cad), "watts": ch("watts", watts), "temp": ch("temp", temp), "grade_smooth": ch("grade_smooth", grade),
	}
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// rideTotals are the summary numbers of the modelled ride.
func rideTotals() (distance, gain, avgSpeed, maxSpeed, avgHR, maxHR, avgWatts, avgCadence float64, maxWatts int) {
	var sumSpeed, sumHR, sumWatts, sumCad float64
	for i := range sampleCount {
		if i > 0 {
			distance += sampleSpeed(i)
		}
		sumSpeed += sampleSpeed(i)
		sumHR += float64(sampleHR(i))
		sumWatts += float64(sampleWatts(i))
		sumCad += float64(sampleCadence(i))
		maxSpeed = max(maxSpeed, sampleSpeed(i))
		maxHR = max(maxHR, float64(sampleHR(i)))
		maxWatts = max(maxWatts, sampleWatts(i))
	}
	n := float64(sampleCount)
	gain = sampleAltitude(sampleCount-1) - sampleAltitude(0)
	return round1(distance), round1(gain), round1(sumSpeed / n), maxSpeed, round1(sumHR / n), maxHR, round1(sumWatts / n), round1(sumCad / n), maxWatts
}

// addActivity stores a ride starting at start; callers hold s.mu.
func (s *Server) addActivity(name string, start time.Time, streams bool) int64 {
	id := s.nextID
	s.nextID++
	start = start.UTC().Truncate(time.Second)
	distance, gain, avgSpeed, maxSpeed, avgHR, maxHR, avgWatts, avgCadence, maxWatts := rideTotals()
	a := &activity{
		ID: id, Name: name, Description: "mock-strava ride", Type: "Ride", SportType: "Ride",
		StartDate: start.Format(time.RFC3339), StartDateLocal: start.Format(time.RFC3339), Timezone: "(GMT+00:00) UTC",
		ElapsedTime: sampleCount, MovingTime: sampleCount, Distance: distance, TotalElevationGain: gain,
		AverageSpeed: avgSpeed, MaxSpeed: maxSpeed, AverageHeartrate: avgHR, MaxHeartrate: maxHR, HasHeartrate: true,
		AverageWatts: avgWatts, WeightedAverageWatts: avgWatts + 5, MaxWatts: maxWatts, DeviceWatts: true,
		AverageCadence: avgCadence, AverageTemp: sampleTemp, Calories: round1(avgWatts * sampleCount / 1000 * 1.1),
		Kilojoules: round1(avgWatts * sampleCount / 1000), GearID: gearBikeID, Gear: summaryGear(gearBikeID),
		Map:   activityMap{ID: "a" + strconv.FormatInt(id, 10), Polyline: segmentPolyline, SummaryPolyline: segmentPolyline},
		Laps:  makeLaps(id, start, distance, avgSpeed, maxSpeed, avgHR, maxHR, avgWatts, avgCadence, gain),
		start: start,
	}
	a.Athlete.ID = s.opts.AthleteID
	a.SegmentEfforts = []segmentEffort{}
	if s.opts.Segments {
		a.SegmentEfforts = []segmentEffort{makeEffort(id, start, avgHR, maxHR, avgWatts, avgCadence)}
	}
	if streams {
		a.streams = makeStreams()
	}
	s.activities = append(s.activities, a)
	return id
}

func makeLaps(id int64, start time.Time, distance, avgSpeed, maxSpeed, avgHR, maxHR, avgWatts, avgCadence, gain float64) []lap {
	laps := make([]lap, 0, lapCount)
	per := sampleCount / lapCount
	for i := range lapCount {
		ls := start.Add(time.Duration(i*per) * time.Second).Format(time.RFC3339)
		laps = append(laps, lap{
			ID: id*10 + int64(i) + 1, LapIndex: i + 1, Name: "Lap " + itoa(i+1), StartDate: ls, StartDateLocal: ls,
			ElapsedTime: per, MovingTime: per, Distance: round1(distance / lapCount), StartIndex: i * per, EndIndex: (i+1)*per - 1,
			AverageSpeed: avgSpeed, MaxSpeed: maxSpeed, AverageHeartrate: avgHR, MaxHeartrate: maxHR, AverageWatts: avgWatts,
			AverageCadence: avgCadence, TotalElevationGain: round1(gain / lapCount),
		})
	}
	return laps
}

func makeEffort(id int64, start time.Time, avgHR, maxHR, avgWatts, avgCadence float64) segmentEffort {
	seg := segment7()
	ls := start.Format(time.RFC3339)
	return segmentEffort{
		ID: id*100 + 1, Name: seg.Name, ElapsedTime: 30, MovingTime: 30, StartDate: ls, StartDateLocal: ls, Distance: seg.Distance,
		StartIndex: 0, EndIndex: 29, AverageHeartrate: avgHR, MaxHeartrate: maxHR, AverageWatts: avgWatts, AverageCadence: avgCadence,
		Segment: seg,
	}
}

func (s *Server) find(id int64) (*activity, bool) {
	for _, a := range s.activities {
		if a.ID == id {
			return a, true
		}
	}
	return nil, false
}

func itoa(i int) string { return strconv.Itoa(i) }

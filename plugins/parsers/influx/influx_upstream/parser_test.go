package influx_upstream

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/influxdata/line-protocol/v2/lineprotocol"
	"github.com/stretchr/testify/require"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/metric"
	"github.com/influxdata/telegraf/testutil"
)

var DefaultTime = func() time.Time {
	return time.Unix(42, 0)
}

type parseTest struct {
	name     string
	input    []byte
	timeFunc TimeFunc
	metrics  []telegraf.Metric
	err      error
}

func parseTests(stream bool) []parseTest {
	// This is required as there is no way to get the internal buffer
	// of the decoder to show where the error occurred. As such, the
	// error `buf` will be empty when decoding from a stream.
	var (
		intOverflowBuf        string
		uintOverflowBuf       string
		invalidMeasurementBuf string
	)
	if stream {
		intOverflowBuf = ""
		uintOverflowBuf = ""
		invalidMeasurementBuf = ""
	} else {
		intOverflowBuf = "cpu value=9223372036854775808i"
		uintOverflowBuf = "cpu value=18446744073709551616u"
		invalidMeasurementBuf = "cpu"
	}

	return []parseTest{
		{
			name:  "minimal",
			input: []byte("cpu value=42 0"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(0, 0),
				),
			},
			err: nil,
		},
		{
			name:  "minimal with newline",
			input: []byte("cpu value=42 0\n"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(0, 0),
				),
			},
			err: nil,
		},
		{
			name:  "measurement escape space",
			input: []byte(`c\ pu value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"c pu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "measurement escape comma",
			input: []byte(`c\,pu value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"c,pu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tags",
			input: []byte(`cpu,cpu=cpu0,host=localhost value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"cpu":  "cpu0",
						"host": "localhost",
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tags escape unescapable",
			input: []byte(`cpu,ho\st=localhost value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						`ho\st`: "localhost",
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tags escape equals",
			input: []byte(`cpu,ho\=st=localhost value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"ho=st": "localhost",
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tags escape comma",
			input: []byte(`cpu,ho\,st=localhost value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"ho,st": "localhost",
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tag value escape space",
			input: []byte(`cpu,host=two\ words value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"host": "two words",
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tag value double escape space",
			input: []byte(`cpu,host=two\\ words value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"host": `two\ words`,
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "tag value triple escape space",
			input: []byte(`cpu,host=two\\\ words value=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"host": `two\\ words`,
					},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field key escape not escapable",
			input: []byte(`cpu va\lue=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						`va\lue`: 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field key escape equals",
			input: []byte(`cpu va\=lue=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						`va=lue`: 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field key escape comma",
			input: []byte(`cpu va\,lue=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						`va,lue`: 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field key escape space",
			input: []byte(`cpu va\ lue=42`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						`va lue`: 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field int",
			input: []byte("cpu value=42i"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:    "field int overflow",
			input:   []byte("cpu value=9223372036854775808i"),
			metrics: nil,
			err: &ParseError{
				DecodeError: &lineprotocol.DecodeError{
					Line:   1,
					Column: 11,
					Err:    errors.New(`cannot parse value for field key "value": line-protocol value out of range`),
				},
				buf: intOverflowBuf,
			},
		},
		{
			name:  "field int max value",
			input: []byte("cpu value=9223372036854775807i"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": int64(9223372036854775807),
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field uint",
			input: []byte("cpu value=42u"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": uint64(42),
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:    "field uint overflow",
			input:   []byte("cpu value=18446744073709551616u"),
			metrics: nil,
			err: &ParseError{
				DecodeError: &lineprotocol.DecodeError{
					Line:   1,
					Column: 11,
					Err:    errors.New(`cannot parse value for field key "value": line-protocol value out of range`),
				},
				buf: uintOverflowBuf,
			},
		},
		{
			name:  "field uint max value",
			input: []byte("cpu value=18446744073709551615u"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": uint64(18446744073709551615),
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field boolean",
			input: []byte("cpu value=true"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": true,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field string",
			input: []byte(`cpu value="42"`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": "42",
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field string escape quote",
			input: []byte(`cpu value="how\"dy"`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						`value`: `how"dy`,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field string escape backslash",
			input: []byte(`cpu value="how\\dy"`),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						`value`: `how\dy`,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "field string newline",
			input: []byte("cpu value=\"4\n2\""),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": "4\n2",
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "no timestamp",
			input: []byte("cpu value=42"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:  "no timestamp",
			input: []byte("cpu value=42"),
			timeFunc: func() time.Time {
				return time.Unix(42, 123456789)
			},
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 123456789),
				),
			},
			err: nil,
		},
		{
			name:  "multiple lines",
			input: []byte("cpu value=42\ncpu value=42"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{
						"value": 42.0,
					},
					time.Unix(42, 0),
				),
			},
			err: nil,
		},
		{
			name:    "invalid measurement only",
			input:   []byte("cpu"),
			metrics: nil,
			err: &ParseError{
				DecodeError: &lineprotocol.DecodeError{
					Line:   1,
					Column: 4,
					Err:    errors.New("empty tag name"),
				},
				buf: invalidMeasurementBuf,
			},
		},
		{
			name: "procstat",
			input: []byte(
				"procstat,exe=bash,process_name=bash voluntary_context_switches=42i,memory_rss=5103616i,rlimit_memory_data_hard=2147483647i," +
					"cpu_time_user=0.02,rlimit_file_locks_soft=2147483647i,pid=29417i,cpu_time_nice=0,rlimit_memory_locked_soft=65536i," +
					"read_count=259i,rlimit_memory_vms_hard=2147483647i,memory_swap=0i,rlimit_num_fds_soft=1024i,rlimit_nice_priority_hard=0i," +
					"cpu_time_soft_irq=0,cpu_time=0i,rlimit_memory_locked_hard=65536i,realtime_priority=0i,signals_pending=0i,nice_priority=20i," +
					"cpu_time_idle=0,memory_stack=139264i,memory_locked=0i,rlimit_memory_stack_soft=8388608i,cpu_time_iowait=0,cpu_time_guest=0," +
					"cpu_time_guest_nice=0,rlimit_memory_data_soft=2147483647i,read_bytes=0i,rlimit_cpu_time_soft=2147483647i," +
					"involuntary_context_switches=2i,write_bytes=106496i,cpu_time_system=0,cpu_time_irq=0,cpu_usage=0,memory_vms=21659648i," +
					"memory_data=1576960i,rlimit_memory_stack_hard=2147483647i,num_threads=1i,rlimit_memory_rss_soft=2147483647i," +
					"rlimit_realtime_priority_soft=0i,num_fds=4i,write_count=35i,rlimit_signals_pending_soft=78994i,cpu_time_steal=0," +
					"rlimit_num_fds_hard=4096i,rlimit_file_locks_hard=2147483647i,rlimit_cpu_time_hard=2147483647i,rlimit_signals_pending_hard=78994i," +
					"rlimit_nice_priority_soft=0i,rlimit_memory_rss_hard=2147483647i,rlimit_memory_vms_soft=2147483647i,rlimit_realtime_priority_hard=0i " +
					"1517620624000000000",
			),
			metrics: []telegraf.Metric{
				metric.New(
					"procstat",
					map[string]string{
						"exe":          "bash",
						"process_name": "bash",
					},
					map[string]interface{}{
						"cpu_time":                      0,
						"cpu_time_guest":                float64(0),
						"cpu_time_guest_nice":           float64(0),
						"cpu_time_idle":                 float64(0),
						"cpu_time_iowait":               float64(0),
						"cpu_time_irq":                  float64(0),
						"cpu_time_nice":                 float64(0),
						"cpu_time_soft_irq":             float64(0),
						"cpu_time_steal":                float64(0),
						"cpu_time_system":               float64(0),
						"cpu_time_user":                 float64(0.02),
						"cpu_usage":                     float64(0),
						"involuntary_context_switches":  2,
						"memory_data":                   1576960,
						"memory_locked":                 0,
						"memory_rss":                    5103616,
						"memory_stack":                  139264,
						"memory_swap":                   0,
						"memory_vms":                    21659648,
						"nice_priority":                 20,
						"num_fds":                       4,
						"num_threads":                   1,
						"pid":                           29417,
						"read_bytes":                    0,
						"read_count":                    259,
						"realtime_priority":             0,
						"rlimit_cpu_time_hard":          2147483647,
						"rlimit_cpu_time_soft":          2147483647,
						"rlimit_file_locks_hard":        2147483647,
						"rlimit_file_locks_soft":        2147483647,
						"rlimit_memory_data_hard":       2147483647,
						"rlimit_memory_data_soft":       2147483647,
						"rlimit_memory_locked_hard":     65536,
						"rlimit_memory_locked_soft":     65536,
						"rlimit_memory_rss_hard":        2147483647,
						"rlimit_memory_rss_soft":        2147483647,
						"rlimit_memory_stack_hard":      2147483647,
						"rlimit_memory_stack_soft":      8388608,
						"rlimit_memory_vms_hard":        2147483647,
						"rlimit_memory_vms_soft":        2147483647,
						"rlimit_nice_priority_hard":     0,
						"rlimit_nice_priority_soft":     0,
						"rlimit_num_fds_hard":           4096,
						"rlimit_num_fds_soft":           1024,
						"rlimit_realtime_priority_hard": 0,
						"rlimit_realtime_priority_soft": 0,
						"rlimit_signals_pending_hard":   78994,
						"rlimit_signals_pending_soft":   78994,
						"signals_pending":               0,
						"voluntary_context_switches":    42,
						"write_bytes":                   106496,
						"write_count":                   35,
					},
					time.Unix(0, 1517620624000000000),
				),
			},
			err: nil,
		},
	}
}

func TestParser(t *testing.T) {
	for _, tt := range parseTests(false) {
		t.Run(tt.name, func(t *testing.T) {
			parser := Parser{}
			require.NoError(t, parser.Init())
			parser.SetTimeFunc(DefaultTime)
			if tt.timeFunc != nil {
				parser.SetTimeFunc(tt.timeFunc)
			}

			metrics, err := parser.Parse(tt.input)
			if tt.err == nil {
				require.NoError(t, err)
			} else {
				require.Equal(t, tt.err.Error(), err.Error())
			}

			require.Len(t, metrics, len(tt.metrics))
			for i, expected := range tt.metrics {
				require.Equal(t, expected.Name(), metrics[i].Name())
				require.Equal(t, expected.Tags(), metrics[i].Tags())
				require.Equal(t, expected.Fields(), metrics[i].Fields())
				require.Equal(t, expected.Time(), metrics[i].Time())
			}
		})
	}
}

func BenchmarkParser(b *testing.B) {
	for _, tt := range parseTests(false) {
		b.Run(tt.name, func(b *testing.B) {
			parser := Parser{}
			require.NoError(b, parser.Init())
			for n := 0; n < b.N; n++ {
				metrics, err := parser.Parse(tt.input)
				_ = err
				_ = metrics
			}
		})
	}
}

func TestStreamParser(t *testing.T) {
	for _, tt := range parseTests(true) {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewBuffer(tt.input)
			parser := NewStreamParser(r)
			parser.SetTimeFunc(DefaultTime)
			if tt.timeFunc != nil {
				parser.SetTimeFunc(tt.timeFunc)
			}

			var i int
			for {
				m, err := parser.Next()
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					require.Equal(t, tt.err.Error(), err.Error())
					break
				}

				testutil.RequireMetricEqual(t, tt.metrics[i], m)
				i++
			}
		})
	}
}

func TestSeriesParser(t *testing.T) {
	var tests = []struct {
		name     string
		input    []byte
		timeFunc func() time.Time
		metrics  []telegraf.Metric
		err      error
	}{
		{
			name:  "empty",
			input: []byte(""),
		},
		{
			name:  "minimal",
			input: []byte("cpu"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]interface{}{},
					time.Unix(0, 0),
				),
			},
		},
		{
			name:  "tags",
			input: []byte("cpu,a=x,b=y"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{
						"a": "x",
						"b": "y",
					},
					map[string]interface{}{},
					time.Unix(0, 0),
				),
			},
		},
		{
			name:  "missing tag value",
			input: []byte("cpu,a="),
			err: &ParseError{
				DecodeError: &lineprotocol.DecodeError{
					Line:   1,
					Column: 7,
					Err:    errors.New(`expected tag value after tag key "a", but none found`),
				},
				buf: "cpu,a=",
			},
		},
		{
			name:  "error with carriage return in long line",
			input: []byte("cpu,a=" + strings.Repeat("x", maxErrorBufferSize) + "\rcd,b"),
			err: &ParseError{
				DecodeError: &lineprotocol.DecodeError{
					Line:   1,
					Column: 1031,
					Err:    errors.New(`expected tag key or field but found '\r' instead`),
				},
				buf: "cpu,a=" + strings.Repeat("x", maxErrorBufferSize) + "\rcd,b",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := Parser{
				Type: "series",
			}
			require.NoError(t, parser.Init())
			if tt.timeFunc != nil {
				parser.SetTimeFunc(tt.timeFunc)
			}

			metrics, err := parser.Parse(tt.input)
			require.Equal(t, tt.err, err)
			if err != nil {
				require.Equal(t, tt.err.Error(), err.Error())
			}

			require.Len(t, metrics, len(tt.metrics))
			for i, expected := range tt.metrics {
				require.Equal(t, expected.Name(), metrics[i].Name())
				require.Equal(t, expected.Tags(), metrics[i].Tags())
			}
		})
	}
}

func TestParserTimestampPrecision(t *testing.T) {
	var tests = []struct {
		name      string
		precision string
		input     []byte
		metrics   []telegraf.Metric
		err       error
	}{
		{
			name:      "default - nanosecond",
			precision: "",
			input:     []byte("cpu value=1 1234567890123123123"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]any{
						"value": float64(1),
					},
					time.Unix(0, 1234567890123123123),
				),
			},
		},
		{
			name:      "nanosecond",
			precision: "1ns",
			input:     []byte("cpu value=2 1234567890123123999"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]any{
						"value": float64(2),
					},
					time.Unix(0, 1234567890123123999),
				),
			},
		},
		{
			name:      "microsecond",
			precision: "1us",
			input:     []byte("cpu value=3 1234567890123123"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]any{
						"value": float64(3),
					},
					time.Unix(0, 1234567890123123000),
				),
			},
		},
		{
			name:      "millisecond",
			precision: "1ms",
			input:     []byte("cpu value=4 1234567890123"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]any{
						"value": float64(4),
					},
					time.Unix(0, 1234567890123000000),
				),
			},
		},
		{
			name:      "second",
			precision: "1s",
			input:     []byte("cpu value=5 1234567890"),
			metrics: []telegraf.Metric{
				metric.New(
					"cpu",
					map[string]string{},
					map[string]any{
						"value": float64(5),
					},
					time.Unix(0, 1234567890000000000),
				),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := config.Duration(0)
			require.NoError(t, d.UnmarshalText([]byte(tt.precision)))
			parser := Parser{InfluxTimestampPrecision: d}
			require.NoError(t, parser.Init())

			metrics, err := parser.Parse(tt.input)
			require.NoError(t, err)

			require.Equal(t, tt.metrics, metrics)
		})
	}
}

func TestParserInvalidTimestampPrecision(t *testing.T) {
	d := config.Duration(0)
	for _, precision := range []string{"1h", "1d", "2s", "1m", "2ns"} {
		require.NoError(t, d.UnmarshalText([]byte(precision)))
		parser := Parser{InfluxTimestampPrecision: d}
		require.ErrorContains(t, parser.Init(), "invalid time precision")
	}
}

func TestParserErrorString(t *testing.T) {
	var ptests = []struct {
		name      string
		input     []byte
		errString string
	}{
		{
			name:      "multiple line error",
			input:     []byte("cpu value=42\ncpu value=invalid\ncpu value=42"),
			errString: `metric parse error: field value has unrecognized type at 2:11: "cpu value=invalid"`,
		},
		{
			name:  "handler error",
			input: []byte("cpu value=9223372036854775808i\ncpu value=42"),
			errString: `metric parse error: cannot parse value for field key "value": ` +
				`line-protocol value out of range at 1:11: "cpu value=9223372036854775808i"`,
		},
		{
			name:      "buffer too long",
			input:     []byte("cpu " + strings.Repeat("ab", maxErrorBufferSize) + "=invalid\ncpu value=42"),
			errString: "metric parse error: field value has unrecognized type at 1:2054: \"...b" + strings.Repeat("ab", maxErrorBufferSize/2-1) + "=<-- here\"",
		},
		{
			name:      "multiple line error",
			input:     []byte("cpu value=42\ncpu value=invalid\ncpu value=42\ncpu value=invalid"),
			errString: `metric parse error: field value has unrecognized type at 2:11: "cpu value=invalid"`,
		},
	}

	for _, tt := range ptests {
		t.Run(tt.name, func(t *testing.T) {
			parser := Parser{}
			require.NoError(t, parser.Init())

			_, err := parser.Parse(tt.input)
			require.Equal(t, tt.errString, err.Error())
		})
	}
}

func TestStreamParserErrorString(t *testing.T) {
	var ptests = []struct {
		name  string
		input []byte
		errs  []string
	}{
		{
			name:  "multiple line error",
			input: []byte("cpu value=42\ncpu value=invalid\ncpu value=42"),
			errs: []string{
				`metric parse error: field value has unrecognized type at 2:11`,
			},
		},
		{
			name:  "handler error",
			input: []byte("cpu value=9223372036854775808i\ncpu value=42"),
			errs: []string{
				`metric parse error: cannot parse value for field key "value": line-protocol value out of range at 1:11`,
			},
		},
		{
			name:  "buffer too long",
			input: []byte("cpu " + strings.Repeat("ab", maxErrorBufferSize) + "=invalid\ncpu value=42"),
			errs: []string{
				"metric parse error: field value has unrecognized type at 1:2054",
			},
		},
		{
			name:  "multiple errors",
			input: []byte("foo value=1asdf2.0\nfoo value=2.0\nfoo value=3asdf2.0\nfoo value=4.0"),
			errs: []string{
				`metric parse error: cannot parse value for field key "value": invalid float value syntax at 1:11`,
				`metric parse error: cannot parse value for field key "value": invalid float value syntax at 3:11`,
			},
		},
	}

	for _, tt := range ptests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewStreamParser(bytes.NewBuffer(tt.input))

			var errs []error
			for i := 0; i < 20; i++ {
				_, err := parser.Next()
				if errors.Is(err, io.EOF) {
					break
				}

				if err != nil {
					errs = append(errs, err)
				}
			}

			require.Len(t, errs, len(tt.errs))
			for i, err := range errs {
				require.Equal(t, tt.errs[i], err.Error())
			}
		})
	}
}

type MockReader struct {
	ReadF func() (int, error)
}

func (r *MockReader) Read([]byte) (int, error) {
	return r.ReadF()
}

// Errors from the Reader are returned from the Parser
func TestStreamParserReaderError(t *testing.T) {
	readerErr := errors.New("error but not eof")

	parser := NewStreamParser(&MockReader{
		ReadF: func() (int, error) {
			return 0, readerErr
		},
	})
	_, err := parser.Next()
	require.Error(t, err)
	require.Equal(t, err, readerErr)

	_, err = parser.Next()
	require.Equal(t, err, io.EOF)
}

func TestStreamParserProducesAllAvailableMetrics(t *testing.T) {
	r, w := io.Pipe()

	parser := NewStreamParser(r)
	parser.SetTimeFunc(DefaultTime)

	ch := make(chan error)
	go func() {
		_, err := w.Write([]byte("metric value=1\nmetric2 value=1\n"))
		ch <- err
		close(ch)
	}()

	_, err := parser.Next()
	require.NoError(t, err)

	// should not block on second read
	_, err = parser.Next()
	require.NoError(t, err)

	err = <-ch
	require.NoError(t, err)
}

const benchmarkData = `benchmark,tags_host=myhost,tags_platform=python,tags_sdkver=3.11.5 value=5 1653643421
benchmark,tags_host=myhost,tags_platform=python,tags_sdkver=3.11.4 value=4 1653643422
`

func TestBenchmarkData(t *testing.T) {
	plugin := &Parser{}
	require.NoError(t, plugin.Init())

	expected := []telegraf.Metric{
		metric.New(
			"benchmark",
			map[string]string{
				"tags_host":     "myhost",
				"tags_platform": "python",
				"tags_sdkver":   "3.11.5",
			},
			map[string]interface{}{
				"value": float64(5),
			},
			time.Unix(1653643422, 0),
		),
		metric.New(
			"benchmark",
			map[string]string{
				"tags_host":     "myhost",
				"tags_platform": "python",
				"tags_sdkver":   "3.11.4",
			},
			map[string]interface{}{
				"value": float64(4),
			},
			time.Unix(1653643422, 0),
		),
	}

	// Do the parsing
	actual, err := plugin.Parse([]byte(benchmarkData))
	require.NoError(t, err)
	testutil.RequireMetricsEqual(t, expected, actual, testutil.IgnoreTime(), testutil.SortMetrics())
}

func BenchmarkParsing(b *testing.B) {
	plugin := &Parser{}
	require.NoError(b, plugin.Init())

	for n := 0; n < b.N; n++ {
		//nolint:errcheck // Benchmarking so skip the error check to avoid the unnecessary operations
		plugin.Parse([]byte(benchmarkData))
	}
}

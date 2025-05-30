//go:generate ../../../tools/readme_config_includer/generator
package bigquery

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/outputs"
)

//go:embed sample.conf
var sampleConfig string

const timeStampFieldName = "timestamp"

var defaultTimeout = config.Duration(5 * time.Second)

type BigQuery struct {
	CredentialsFile string `toml:"credentials_file"`
	Project         string `toml:"project"`
	Dataset         string `toml:"dataset"`

	Timeout         config.Duration `toml:"timeout"`
	ReplaceHyphenTo string          `toml:"replace_hyphen_to"`
	CompactTable    string          `toml:"compact_table"`

	Log telegraf.Logger `toml:"-"`

	client *bigquery.Client

	warnedOnHyphens map[string]bool
}

func (*BigQuery) SampleConfig() string {
	return sampleConfig
}

func (b *BigQuery) Init() error {
	if b.Project == "" {
		b.Project = bigquery.DetectProjectID
	}

	if b.Dataset == "" {
		return errors.New(`"dataset" is required`)
	}

	b.warnedOnHyphens = make(map[string]bool)

	return nil
}

func (b *BigQuery) Connect() error {
	if b.client == nil {
		if err := b.setUpDefaultClient(); err != nil {
			return err
		}
	}

	if b.CompactTable != "" {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, time.Duration(b.Timeout))
		defer cancel()

		// Check if the compact table exists
		_, err := b.client.Dataset(b.Dataset).Table(b.CompactTable).Metadata(ctx)
		if err != nil {
			return fmt.Errorf("compact table: %w", err)
		}
	}
	return nil
}

func (b *BigQuery) setUpDefaultClient() error {
	var credentialsOption option.ClientOption

	// https://cloud.google.com/go/docs/reference/cloud.google.com/go/0.94.1#hdr-Timeouts_and_Cancellation
	// Do not attempt to add timeout to this context for the bigquery client.
	ctx := context.Background()

	if b.CredentialsFile != "" {
		credentialsOption = option.WithCredentialsFile(b.CredentialsFile)
	} else {
		creds, err := google.FindDefaultCredentials(ctx, bigquery.Scope)
		if err != nil {
			return fmt.Errorf(
				"unable to find Google Cloud Platform Application Default Credentials: %w. "+
					"Either set ADC or provide CredentialsFile config", err)
		}
		credentialsOption = option.WithCredentials(creds)
	}

	client, err := bigquery.NewClient(ctx, b.Project,
		credentialsOption,
		option.WithUserAgent(internal.ProductToken()),
	)
	b.client = client
	return err
}

// Write the metrics to Google Cloud BigQuery.
func (b *BigQuery) Write(metrics []telegraf.Metric) error {
	if b.CompactTable != "" {
		return b.writeCompact(metrics)
	}

	groupedMetrics := groupByMetricName(metrics)

	var wg sync.WaitGroup

	for k, v := range groupedMetrics {
		wg.Add(1)
		go func(k string, v []bigquery.ValueSaver) {
			defer wg.Done()
			b.insertToTable(k, v)
		}(k, v)
	}

	wg.Wait()

	return nil
}

func (b *BigQuery) writeCompact(metrics []telegraf.Metric) error {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(b.Timeout))
	defer cancel()

	// Always returns an instance, even if table doesn't exist (anymore).
	inserter := b.client.Dataset(b.Dataset).Table(b.CompactTable).Inserter()

	var compactValues []*bigquery.ValuesSaver
	for _, m := range metrics {
		valueSaver, err := b.newCompactValuesSaver(m)
		if err != nil {
			b.Log.Warnf("could not prepare metric as compact value: %v", err)
		} else {
			compactValues = append(compactValues, valueSaver)
		}
	}
	return inserter.Put(ctx, compactValues)
}

func groupByMetricName(metrics []telegraf.Metric) map[string][]bigquery.ValueSaver {
	groupedMetrics := make(map[string][]bigquery.ValueSaver)

	for _, m := range metrics {
		bqm := newValuesSaver(m)
		groupedMetrics[m.Name()] = append(groupedMetrics[m.Name()], bqm)
	}

	return groupedMetrics
}

func newValuesSaver(m telegraf.Metric) *bigquery.ValuesSaver {
	s := make(bigquery.Schema, 0)
	r := make([]bigquery.Value, 0)
	timeSchema := timeStampFieldSchema()
	s = append(s, timeSchema)
	r = append(r, m.Time())

	s, r = tagsSchemaAndValues(m, s, r)
	s, r = valuesSchemaAndValues(m, s, r)

	return &bigquery.ValuesSaver{
		Schema: s.Relax(),
		Row:    r,
	}
}

func (b *BigQuery) newCompactValuesSaver(m telegraf.Metric) (*bigquery.ValuesSaver, error) {
	tags, err := json.Marshal(m.Tags())
	if err != nil {
		return nil, fmt.Errorf("serializing tags: %w", err)
	}

	rawFields := make(map[string]interface{}, len(m.FieldList()))
	for _, field := range m.FieldList() {
		if fv, ok := field.Value.(float64); ok {
			// JSON does not support these special values
			if math.IsNaN(fv) || math.IsInf(fv, 0) {
				b.Log.Debugf("Ignoring unsupported field %s with value %q for metric %s", field.Key, field.Value, m.Name())
				continue
			}
		}
		rawFields[field.Key] = field.Value
	}
	fields, err := json.Marshal(rawFields)
	if err != nil {
		return nil, fmt.Errorf("serializing fields: %w", err)
	}

	return &bigquery.ValuesSaver{
		Schema: bigquery.Schema{
			timeStampFieldSchema(),
			newStringFieldSchema("name"),
			newJSONFieldSchema("tags"),
			newJSONFieldSchema("fields"),
		},
		Row: []bigquery.Value{
			m.Time(),
			m.Name(),
			string(tags),
			string(fields),
		},
	}, nil
}

func timeStampFieldSchema() *bigquery.FieldSchema {
	return &bigquery.FieldSchema{
		Name: timeStampFieldName,
		Type: bigquery.TimestampFieldType,
	}
}

func newStringFieldSchema(name string) *bigquery.FieldSchema {
	return &bigquery.FieldSchema{
		Name: name,
		Type: bigquery.StringFieldType,
	}
}

func newJSONFieldSchema(name string) *bigquery.FieldSchema {
	return &bigquery.FieldSchema{
		Name: name,
		Type: bigquery.JSONFieldType,
	}
}

func tagsSchemaAndValues(m telegraf.Metric, s bigquery.Schema, r []bigquery.Value) ([]*bigquery.FieldSchema, []bigquery.Value) {
	for _, t := range m.TagList() {
		s = append(s, newStringFieldSchema(t.Key))
		r = append(r, t.Value)
	}

	return s, r
}

func valuesSchemaAndValues(m telegraf.Metric, s bigquery.Schema, r []bigquery.Value) ([]*bigquery.FieldSchema, []bigquery.Value) {
	for _, f := range m.FieldList() {
		s = append(s, valuesSchema(f))
		r = append(r, f.Value)
	}

	return s, r
}

func valuesSchema(f *telegraf.Field) *bigquery.FieldSchema {
	return &bigquery.FieldSchema{
		Name: f.Key,
		Type: valueToBqType(f.Value),
	}
}

func valueToBqType(v interface{}) bigquery.FieldType {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int64:
		return bigquery.IntegerFieldType
	case reflect.Float32, reflect.Float64:
		return bigquery.FloatFieldType
	case reflect.Bool:
		return bigquery.BooleanFieldType
	default:
		return bigquery.StringFieldType
	}
}

func (b *BigQuery) insertToTable(metricName string, metrics []bigquery.ValueSaver) {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(b.Timeout))
	defer cancel()

	tableName := b.metricToTable(metricName)
	table := b.client.Dataset(b.Dataset).Table(tableName)
	inserter := table.Inserter()

	if err := inserter.Put(ctx, metrics); err != nil {
		b.Log.Errorf("inserting metric %q failed: %v", metricName, err)
	}
}

func (b *BigQuery) metricToTable(metricName string) string {
	if !strings.Contains(metricName, "-") {
		return metricName
	}

	dhm := strings.ReplaceAll(metricName, "-", b.ReplaceHyphenTo)

	if warned := b.warnedOnHyphens[metricName]; !warned {
		b.Log.Warnf("Metric %q contains hyphens please consider using the rename processor plugin, falling back to %q", metricName, dhm)
		b.warnedOnHyphens[metricName] = true
	}

	return dhm
}

// Close will terminate the session to the backend, returning error if an issue arises.
func (b *BigQuery) Close() error {
	return b.client.Close()
}

func init() {
	outputs.Add("bigquery", func() telegraf.Output {
		return &BigQuery{
			Timeout:         defaultTimeout,
			ReplaceHyphenTo: "_",
		}
	})
}

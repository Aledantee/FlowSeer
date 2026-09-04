package service

import (
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestTransformMetricSupportsEveryAggregation(t *testing.T) {
	start := time.Unix(1, 2)
	end := time.Unix(3, 4)
	attributes := attribute.NewSet(attribute.String("flowseer.test.dimension", "value"))
	traceID := []byte("0123456789abcdef")
	spanID := []byte("01234567")

	intExemplar := metricdata.Exemplar[int64]{
		FilteredAttributes: []attribute.KeyValue{attribute.String("flowseer.test.filtered", "int")},
		Time:               end,
		Value:              7,
		TraceID:            traceID,
		SpanID:             spanID,
	}
	floatExemplar := metricdata.Exemplar[float64]{
		FilteredAttributes: []attribute.KeyValue{attribute.String("flowseer.test.filtered", "float")},
		Time:               end,
		Value:              7.5,
		TraceID:            traceID,
		SpanID:             spanID,
	}

	tests := []struct {
		name  string
		data  metricdata.Aggregation
		check func(*testing.T, *metricspb.Metric)
	}{
		{
			name: "int64 gauge",
			data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{
				Attributes: attributes, StartTime: start, Time: end, Value: 11, Exemplars: []metricdata.Exemplar[int64]{intExemplar},
			}}},
			check: func(t *testing.T, metric *metricspb.Metric) {
				point := singleNumberPoint(t, metric.GetGauge().GetDataPoints())
				if got := point.GetAsInt(); got != 11 {
					t.Errorf("int64 gauge value = %d, want 11", got)
				}
				assertNumberPointMetadata(t, point, start, end, traceID, spanID)
			},
		},
		{
			name: "float64 gauge",
			data: metricdata.Gauge[float64]{DataPoints: []metricdata.DataPoint[float64]{{
				Attributes: attributes, StartTime: start, Time: end, Value: 11.5, Exemplars: []metricdata.Exemplar[float64]{floatExemplar},
			}}},
			check: func(t *testing.T, metric *metricspb.Metric) {
				point := singleNumberPoint(t, metric.GetGauge().GetDataPoints())
				if got := point.GetAsDouble(); got != 11.5 {
					t.Errorf("float64 gauge value = %v, want 11.5", got)
				}
				assertNumberPointMetadata(t, point, start, end, traceID, spanID)
			},
		},
		{
			name: "int64 sum",
			data: metricdata.Sum[int64]{
				Temporality: metricdata.DeltaTemporality, IsMonotonic: true,
				DataPoints: []metricdata.DataPoint[int64]{{Attributes: attributes, StartTime: start, Time: end, Value: 13, Exemplars: []metricdata.Exemplar[int64]{intExemplar}}},
			},
			check: func(t *testing.T, metric *metricspb.Metric) {
				sum := metric.GetSum()
				if sum.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA || !sum.GetIsMonotonic() {
					t.Errorf("int64 sum temporality/monotonic = %v/%v", sum.GetAggregationTemporality(), sum.GetIsMonotonic())
				}
				if got := singleNumberPoint(t, sum.GetDataPoints()).GetAsInt(); got != 13 {
					t.Errorf("int64 sum value = %d, want 13", got)
				}
			},
		},
		{
			name: "float64 sum",
			data: metricdata.Sum[float64]{
				Temporality: metricdata.CumulativeTemporality,
				DataPoints:  []metricdata.DataPoint[float64]{{Attributes: attributes, StartTime: start, Time: end, Value: 13.5, Exemplars: []metricdata.Exemplar[float64]{floatExemplar}}},
			},
			check: func(t *testing.T, metric *metricspb.Metric) {
				sum := metric.GetSum()
				if sum.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE || sum.GetIsMonotonic() {
					t.Errorf("float64 sum temporality/monotonic = %v/%v", sum.GetAggregationTemporality(), sum.GetIsMonotonic())
				}
				if got := singleNumberPoint(t, sum.GetDataPoints()).GetAsDouble(); got != 13.5 {
					t.Errorf("float64 sum value = %v, want 13.5", got)
				}
			},
		},
		{
			name: "int64 histogram",
			data: metricdata.Histogram[int64]{
				Temporality: metricdata.DeltaTemporality,
				DataPoints: []metricdata.HistogramDataPoint[int64]{{
					Attributes: attributes, StartTime: start, Time: end, Count: 2, Sum: 8,
					Bounds: []float64{3}, BucketCounts: []uint64{1, 1},
					Min: metricdata.NewExtrema[int64](2), Max: metricdata.NewExtrema[int64](6), Exemplars: []metricdata.Exemplar[int64]{intExemplar},
				}},
			},
			check: func(t *testing.T, metric *metricspb.Metric) {
				histogram := metric.GetHistogram()
				if histogram.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
					t.Errorf("int64 histogram temporality = %v", histogram.GetAggregationTemporality())
				}
				assertHistogramPoint(t, histogram.GetDataPoints(), 8, 2, 6, []float64{3}, []uint64{1, 1})
			},
		},
		{
			name: "float64 histogram",
			data: metricdata.Histogram[float64]{
				Temporality: metricdata.CumulativeTemporality,
				DataPoints: []metricdata.HistogramDataPoint[float64]{{
					Attributes: attributes, StartTime: start, Time: end, Count: 3, Sum: 9.5,
					Bounds: []float64{2.5, 5}, BucketCounts: []uint64{1, 1, 1},
					Min: metricdata.NewExtrema(1.5), Max: metricdata.NewExtrema(5.5), Exemplars: []metricdata.Exemplar[float64]{floatExemplar},
				}},
			},
			check: func(t *testing.T, metric *metricspb.Metric) {
				histogram := metric.GetHistogram()
				if histogram.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
					t.Errorf("float64 histogram temporality = %v", histogram.GetAggregationTemporality())
				}
				assertHistogramPoint(t, histogram.GetDataPoints(), 9.5, 1.5, 5.5, []float64{2.5, 5}, []uint64{1, 1, 1})
			},
		},
		{
			name: "int64 exponential histogram",
			data: metricdata.ExponentialHistogram[int64]{
				Temporality: metricdata.DeltaTemporality,
				DataPoints: []metricdata.ExponentialHistogramDataPoint[int64]{{
					Attributes: attributes, StartTime: start, Time: end, Count: 4, Sum: 10,
					Min: metricdata.NewExtrema[int64](-2), Max: metricdata.NewExtrema[int64](7), Scale: 3,
					ZeroCount: 1, ZeroThreshold: 0.01,
					PositiveBucket: metricdata.ExponentialBucket{Offset: 2, Counts: []uint64{1, 2}},
					NegativeBucket: metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1}}, Exemplars: []metricdata.Exemplar[int64]{intExemplar},
				}},
			},
			check: func(t *testing.T, metric *metricspb.Metric) {
				histogram := metric.GetExponentialHistogram()
				if histogram.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
					t.Errorf("int64 exponential histogram temporality = %v", histogram.GetAggregationTemporality())
				}
				assertExponentialHistogramPoint(t, histogram.GetDataPoints(), 10, -2, 7)
			},
		},
		{
			name: "float64 exponential histogram",
			data: metricdata.ExponentialHistogram[float64]{
				Temporality: metricdata.CumulativeTemporality,
				DataPoints: []metricdata.ExponentialHistogramDataPoint[float64]{{
					Attributes: attributes, StartTime: start, Time: end, Count: 4, Sum: 10.5,
					Min: metricdata.NewExtrema(-2.5), Max: metricdata.NewExtrema(7.5), Scale: 3,
					ZeroCount: 1, ZeroThreshold: 0.01,
					PositiveBucket: metricdata.ExponentialBucket{Offset: 2, Counts: []uint64{1, 2}},
					NegativeBucket: metricdata.ExponentialBucket{Offset: -1, Counts: []uint64{1}}, Exemplars: []metricdata.Exemplar[float64]{floatExemplar},
				}},
			},
			check: func(t *testing.T, metric *metricspb.Metric) {
				histogram := metric.GetExponentialHistogram()
				if histogram.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
					t.Errorf("float64 exponential histogram temporality = %v", histogram.GetAggregationTemporality())
				}
				assertExponentialHistogramPoint(t, histogram.GetDataPoints(), 10.5, -2.5, 7.5)
			},
		},
		{
			name: "summary",
			data: metricdata.Summary{DataPoints: []metricdata.SummaryDataPoint{{
				Attributes: attributes, StartTime: start, Time: end, Count: 5, Sum: 12.5,
				QuantileValues: []metricdata.QuantileValue{{Quantile: 0.5, Value: 2.5}, {Quantile: 0.9, Value: 4.5}},
			}}},
			check: func(t *testing.T, metric *metricspb.Metric) {
				points := metric.GetSummary().GetDataPoints()
				if len(points) != 1 {
					t.Fatalf("summary points = %d, want 1", len(points))
				}
				point := points[0]
				if point.GetCount() != 5 || point.GetSum() != 12.5 || len(point.GetQuantileValues()) != 2 {
					t.Errorf("summary count/sum/quantiles = %d/%v/%v", point.GetCount(), point.GetSum(), point.GetQuantileValues())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric, err := transformMetric(metricdata.Metrics{Name: tt.name, Description: "description", Unit: "1", Data: tt.data})
			if err != nil {
				t.Fatalf("transformMetric() error: %v", err)
			}
			if metric.GetName() != tt.name || metric.GetDescription() != "description" || metric.GetUnit() != "1" {
				t.Errorf("metric metadata = %q/%q/%q", metric.GetName(), metric.GetDescription(), metric.GetUnit())
			}
			tt.check(t, metric)
		})
	}
}

func singleNumberPoint(t *testing.T, points []*metricspb.NumberDataPoint) *metricspb.NumberDataPoint {
	t.Helper()
	if len(points) != 1 {
		t.Fatalf("number points = %d, want 1", len(points))
	}
	return points[0]
}

func assertNumberPointMetadata(t *testing.T, point *metricspb.NumberDataPoint, start, end time.Time, traceID, spanID []byte) {
	t.Helper()
	if point.GetStartTimeUnixNano() != uint64(start.UnixNano()) || point.GetTimeUnixNano() != uint64(end.UnixNano()) {
		t.Errorf("number point times = %d/%d", point.GetStartTimeUnixNano(), point.GetTimeUnixNano())
	}
	if got := point.GetAttributes(); len(got) != 1 || got[0].GetKey() != "flowseer.test.dimension" {
		t.Errorf("number point attributes = %v", got)
	}
	if exemplars := point.GetExemplars(); len(exemplars) != 1 || !reflect.DeepEqual(exemplars[0].GetTraceId(), traceID) || !reflect.DeepEqual(exemplars[0].GetSpanId(), spanID) {
		t.Errorf("number point exemplars = %v", exemplars)
	}
}

func assertHistogramPoint(t *testing.T, points []*metricspb.HistogramDataPoint, sum, minimum, maximum float64, bounds []float64, buckets []uint64) {
	t.Helper()
	if len(points) != 1 {
		t.Fatalf("histogram points = %d, want 1", len(points))
	}
	point := points[0]
	if point.GetCount() == 0 || point.GetSum() != sum || point.GetMin() != minimum || point.GetMax() != maximum {
		t.Errorf("histogram count/sum/min/max = %d/%v/%v/%v", point.GetCount(), point.GetSum(), point.GetMin(), point.GetMax())
	}
	if !reflect.DeepEqual(point.GetExplicitBounds(), bounds) || !reflect.DeepEqual(point.GetBucketCounts(), buckets) || len(point.GetExemplars()) != 1 {
		t.Errorf("histogram bounds/buckets/exemplars = %v/%v/%v", point.GetExplicitBounds(), point.GetBucketCounts(), point.GetExemplars())
	}
}

func assertExponentialHistogramPoint(t *testing.T, points []*metricspb.ExponentialHistogramDataPoint, sum, minimum, maximum float64) {
	t.Helper()
	if len(points) != 1 {
		t.Fatalf("exponential histogram points = %d, want 1", len(points))
	}
	point := points[0]
	if point.GetCount() != 4 || point.GetSum() != sum || point.GetMin() != minimum || point.GetMax() != maximum || point.GetScale() != 3 || point.GetZeroCount() != 1 || point.GetZeroThreshold() != 0.01 {
		t.Errorf("exponential histogram point = %#v", point)
	}
	if point.GetPositive().GetOffset() != 2 || !reflect.DeepEqual(point.GetPositive().GetBucketCounts(), []uint64{1, 2}) || point.GetNegative().GetOffset() != -1 || !reflect.DeepEqual(point.GetNegative().GetBucketCounts(), []uint64{1}) || len(point.GetExemplars()) != 1 {
		t.Errorf("exponential histogram buckets/exemplars = %v/%v/%v", point.GetPositive(), point.GetNegative(), point.GetExemplars())
	}
}

package service

import (
	"errors"
	"math"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	apilog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func prohibitedTelemetryKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{
		"authorization", "credential", "password", "passwd", "secret", "token",
		"api_key", "apikey", "api-key", "private_key", "client_key", "payload",
		"carrier", "traceparent", "tracestate", "baggage", "stack", "attributes",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func containsTelemetrySecretMarker(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{
		"authorization", "credential", "password", "passwd", "secret", "token",
		"private key", "api_key", "api-key", "apikey", "traceparent", "tracestate",
		"baggage", "payload", "carrier", "do-not-leak",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func transformLogRecords(records []sdklog.Record) *collectorlogspb.ExportLogsServiceRequest {
	if len(records) == 0 {
		return &collectorlogspb.ExportLogsServiceRequest{}
	}
	resourceLogs := make(map[attribute.Distinct]*logspb.ResourceLogs)
	type scopeKey struct {
		resource attribute.Distinct
		name     string
		version  string
		schema   string
	}
	scopeLogs := make(map[scopeKey]*logspb.ScopeLogs)
	for _, record := range records {
		res := record.Resource()
		resourceKey := res.Equivalent()
		resourceGroup := resourceLogs[resourceKey]
		if resourceGroup == nil {
			resourceGroup = &logspb.ResourceLogs{
				Resource:  &resourcepb.Resource{Attributes: otlpAttributeIterator(res.Iter())},
				SchemaUrl: res.SchemaURL(),
			}
			resourceLogs[resourceKey] = resourceGroup
		}
		scope := record.InstrumentationScope()
		key := scopeKey{resource: resourceKey, name: scope.Name, version: scope.Version, schema: scope.SchemaURL}
		scopeGroup := scopeLogs[key]
		if scopeGroup == nil {
			scopeGroup = &logspb.ScopeLogs{
				Scope: &commonpb.InstrumentationScope{
					Name:       sanitizeTelemetryString(scope.Name),
					Version:    sanitizeTelemetryString(scope.Version),
					Attributes: otlpAttributeIterator(scope.Attributes.Iter()),
				},
				SchemaUrl: scope.SchemaURL,
			}
			scopeLogs[key] = scopeGroup
			resourceGroup.ScopeLogs = append(resourceGroup.ScopeLogs, scopeGroup)
		}
		scopeGroup.LogRecords = append(scopeGroup.LogRecords, transformLogRecord(record))
	}
	request := &collectorlogspb.ExportLogsServiceRequest{ResourceLogs: make([]*logspb.ResourceLogs, 0, len(resourceLogs))}
	for _, logs := range resourceLogs {
		request.ResourceLogs = append(request.ResourceLogs, logs)
	}
	return request
}

func transformLogRecord(record sdklog.Record) *logspb.LogRecord {
	attrs := make([]attribute.KeyValue, 0, min(record.AttributesLen(), telemetryAttributeLimit))
	record.WalkAttributes(func(value attribute.KeyValue) bool {
		if len(attrs) < telemetryAttributeLimit && !prohibitedTelemetryKey(string(value.Key)) {
			attrs = append(attrs, value)
		}
		return len(attrs) < telemetryAttributeLimit
	})
	result := &logspb.LogRecord{
		TimeUnixNano:           telemetryUnixNano(record.Timestamp()),
		ObservedTimeUnixNano:   telemetryUnixNano(record.ObservedTimestamp()),
		EventName:              sanitizeTelemetryString(record.EventName()),
		SeverityNumber:         transformSeverity(record.Severity()),
		SeverityText:           sanitizeTelemetryString(record.SeverityText()),
		Body:                   otlpAttributeValue(record.Body()),
		Attributes:             otlpAttributes(attrs),
		DroppedAttributesCount: clampTelemetryUint32(record.DroppedAttributes()),
		Flags:                  uint32(record.TraceFlags()),
	}
	if traceID := record.TraceID(); traceID.IsValid() {
		result.TraceId = traceID[:]
	}
	if spanID := record.SpanID(); spanID.IsValid() {
		result.SpanId = spanID[:]
	}
	return result
}

func transformSeverity(severity apilog.Severity) logspb.SeverityNumber {
	if severity < apilog.SeverityTrace || severity > apilog.SeverityFatal4 {
		return logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED
	}
	return logspb.SeverityNumber(severity)
}

func transformResourceMetrics(data *metricdata.ResourceMetrics) (*collectormetricspb.ExportMetricsServiceRequest, error) {
	resourceMetrics := &metricspb.ResourceMetrics{
		Resource:  &resourcepb.Resource{Attributes: otlpAttributeIterator(data.Resource.Iter())},
		SchemaUrl: data.Resource.SchemaURL(),
	}
	var transformErr error
	for _, scope := range data.ScopeMetrics {
		scopeMetrics := &metricspb.ScopeMetrics{
			Scope: &commonpb.InstrumentationScope{
				Name:       sanitizeTelemetryString(scope.Scope.Name),
				Version:    sanitizeTelemetryString(scope.Scope.Version),
				Attributes: otlpAttributeIterator(scope.Scope.Attributes.Iter()),
			},
			SchemaUrl: scope.Scope.SchemaURL,
		}
		for _, value := range scope.Metrics {
			metric, err := transformMetric(value)
			if err != nil {
				transformErr = errors.Join(transformErr, err)
				continue
			}
			scopeMetrics.Metrics = append(scopeMetrics.Metrics, metric)
		}
		resourceMetrics.ScopeMetrics = append(resourceMetrics.ScopeMetrics, scopeMetrics)
	}
	return &collectormetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{resourceMetrics}}, transformErr
}

func transformMetric(value metricdata.Metrics) (*metricspb.Metric, error) {
	metric := &metricspb.Metric{
		Name:        sanitizeTelemetryString(value.Name),
		Description: sanitizeTelemetryString(value.Description),
		Unit:        sanitizeTelemetryString(value.Unit),
	}
	var err error
	switch data := value.Data.(type) {
	case metricdata.Gauge[int64]:
		metric.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: transformNumberPoints(data.DataPoints)}}
	case metricdata.Gauge[float64]:
		metric.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: transformNumberPoints(data.DataPoints)}}
	case metricdata.Sum[int64]:
		metric.Data, err = transformSum(data)
	case metricdata.Sum[float64]:
		metric.Data, err = transformSum(data)
	case metricdata.Histogram[int64]:
		metric.Data, err = transformHistogram(data)
	case metricdata.Histogram[float64]:
		metric.Data, err = transformHistogram(data)
	case metricdata.ExponentialHistogram[int64]:
		metric.Data, err = transformExponentialHistogram(data)
	case metricdata.ExponentialHistogram[float64]:
		metric.Data, err = transformExponentialHistogram(data)
	case metricdata.Summary:
		metric.Data = transformSummary(data)
	default:
		err = errors.New("unsupported metric aggregation")
	}
	return metric, err
}

func transformSum[N int64 | float64](data metricdata.Sum[N]) (*metricspb.Metric_Sum, error) {
	temporality, err := transformTemporality(data.Temporality)
	if err != nil {
		return nil, err
	}
	return &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		AggregationTemporality: temporality,
		IsMonotonic:            data.IsMonotonic,
		DataPoints:             transformNumberPoints(data.DataPoints),
	}}, nil
}

func transformNumberPoints[N int64 | float64](points []metricdata.DataPoint[N]) []*metricspb.NumberDataPoint {
	result := make([]*metricspb.NumberDataPoint, 0, len(points))
	for _, point := range points {
		converted := &metricspb.NumberDataPoint{
			Attributes:        otlpAttributeIterator(point.Attributes.Iter()),
			StartTimeUnixNano: telemetryUnixNano(point.StartTime),
			TimeUnixNano:      telemetryUnixNano(point.Time),
			Exemplars:         transformExemplars(point.Exemplars),
		}
		switch value := any(point.Value).(type) {
		case int64:
			converted.Value = &metricspb.NumberDataPoint_AsInt{AsInt: value}
		case float64:
			converted.Value = &metricspb.NumberDataPoint_AsDouble{AsDouble: value}
		}
		result = append(result, converted)
	}
	return result
}

func transformHistogram[N int64 | float64](data metricdata.Histogram[N]) (*metricspb.Metric_Histogram, error) {
	temporality, err := transformTemporality(data.Temporality)
	if err != nil {
		return nil, err
	}
	points := make([]*metricspb.HistogramDataPoint, 0, len(data.DataPoints))
	for _, point := range data.DataPoints {
		sum := float64(point.Sum)
		converted := &metricspb.HistogramDataPoint{
			Attributes:        otlpAttributeIterator(point.Attributes.Iter()),
			StartTimeUnixNano: telemetryUnixNano(point.StartTime),
			TimeUnixNano:      telemetryUnixNano(point.Time),
			Count:             point.Count,
			Sum:               &sum,
			BucketCounts:      append([]uint64(nil), point.BucketCounts...),
			ExplicitBounds:    append([]float64(nil), point.Bounds...),
			Exemplars:         transformExemplars(point.Exemplars),
		}
		if minimum, ok := point.Min.Value(); ok {
			value := float64(minimum)
			converted.Min = &value
		}
		if maximum, ok := point.Max.Value(); ok {
			value := float64(maximum)
			converted.Max = &value
		}
		points = append(points, converted)
	}
	return &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{AggregationTemporality: temporality, DataPoints: points}}, nil
}

func transformExponentialHistogram[N int64 | float64](data metricdata.ExponentialHistogram[N]) (*metricspb.Metric_ExponentialHistogram, error) {
	temporality, err := transformTemporality(data.Temporality)
	if err != nil {
		return nil, err
	}
	points := make([]*metricspb.ExponentialHistogramDataPoint, 0, len(data.DataPoints))
	for _, point := range data.DataPoints {
		sum := float64(point.Sum)
		converted := &metricspb.ExponentialHistogramDataPoint{
			Attributes:        otlpAttributeIterator(point.Attributes.Iter()),
			StartTimeUnixNano: telemetryUnixNano(point.StartTime),
			TimeUnixNano:      telemetryUnixNano(point.Time),
			Count:             point.Count,
			Sum:               &sum,
			Scale:             point.Scale,
			ZeroCount:         point.ZeroCount,
			ZeroThreshold:     point.ZeroThreshold,
			Positive:          transformExponentialBuckets(point.PositiveBucket),
			Negative:          transformExponentialBuckets(point.NegativeBucket),
			Exemplars:         transformExemplars(point.Exemplars),
		}
		if minimum, ok := point.Min.Value(); ok {
			value := float64(minimum)
			converted.Min = &value
		}
		if maximum, ok := point.Max.Value(); ok {
			value := float64(maximum)
			converted.Max = &value
		}
		points = append(points, converted)
	}
	return &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{AggregationTemporality: temporality, DataPoints: points}}, nil
}

func transformExponentialBuckets(value metricdata.ExponentialBucket) *metricspb.ExponentialHistogramDataPoint_Buckets {
	return &metricspb.ExponentialHistogramDataPoint_Buckets{Offset: value.Offset, BucketCounts: append([]uint64(nil), value.Counts...)}
}

func transformSummary(data metricdata.Summary) *metricspb.Metric_Summary {
	points := make([]*metricspb.SummaryDataPoint, 0, len(data.DataPoints))
	for _, point := range data.DataPoints {
		converted := &metricspb.SummaryDataPoint{
			Attributes:        otlpAttributeIterator(point.Attributes.Iter()),
			StartTimeUnixNano: telemetryUnixNano(point.StartTime),
			TimeUnixNano:      telemetryUnixNano(point.Time),
			Count:             point.Count,
			Sum:               point.Sum,
		}
		for _, quantile := range point.QuantileValues {
			converted.QuantileValues = append(converted.QuantileValues, &metricspb.SummaryDataPoint_ValueAtQuantile{Quantile: quantile.Quantile, Value: quantile.Value})
		}
		points = append(points, converted)
	}
	return &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: points}}
}

func transformExemplars[N int64 | float64](values []metricdata.Exemplar[N]) []*metricspb.Exemplar {
	result := make([]*metricspb.Exemplar, 0, len(values))
	for _, exemplar := range values {
		converted := &metricspb.Exemplar{
			FilteredAttributes: otlpAttributes(exemplar.FilteredAttributes),
			TimeUnixNano:       telemetryUnixNano(exemplar.Time),
			SpanId:             append([]byte(nil), exemplar.SpanID...),
			TraceId:            append([]byte(nil), exemplar.TraceID...),
		}
		switch value := any(exemplar.Value).(type) {
		case int64:
			converted.Value = &metricspb.Exemplar_AsInt{AsInt: value}
		case float64:
			converted.Value = &metricspb.Exemplar_AsDouble{AsDouble: value}
		}
		result = append(result, converted)
	}
	return result
}

func transformTemporality(value metricdata.Temporality) (metricspb.AggregationTemporality, error) {
	switch value {
	case metricdata.DeltaTemporality:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, nil
	case metricdata.CumulativeTemporality:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, nil
	default:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED, errors.New("unsupported metric temporality")
	}
}

// sanitizeResourceSpans redacts and bounds a trace export in place before the
// transport observes it.
func sanitizeResourceSpans(resources []*tracepb.ResourceSpans) {
	for _, resource := range resources {
		if resource.Resource != nil {
			resource.Resource.Attributes = sanitizeOTLPKeyValues(resource.Resource.Attributes)
		}
		for _, scope := range resource.ScopeSpans {
			if scope.Scope != nil {
				scope.Scope.Name = sanitizeTelemetryString(scope.Scope.Name)
				scope.Scope.Version = sanitizeTelemetryString(scope.Scope.Version)
				scope.Scope.Attributes = sanitizeOTLPKeyValues(scope.Scope.Attributes)
			}
			for _, span := range scope.Spans {
				span.Name = sanitizeTelemetryString(span.Name)
				span.Attributes = sanitizeOTLPKeyValues(span.Attributes)
				if len(span.Events) > telemetryAttributeLimit {
					span.Events = span.Events[:telemetryAttributeLimit]
				}
				for _, event := range span.Events {
					event.Name = sanitizeTelemetryString(event.Name)
					event.Attributes = sanitizeOTLPKeyValues(event.Attributes)
				}
				if len(span.Links) > telemetryAttributeLimit {
					span.Links = span.Links[:telemetryAttributeLimit]
				}
				for _, link := range span.Links {
					link.Attributes = sanitizeOTLPKeyValues(link.Attributes)
				}
				if span.Status != nil {
					span.Status.Message = sanitizeTelemetryString(span.Status.Message)
				}
			}
		}
	}
}

func sanitizeOTLPKeyValues(values []*commonpb.KeyValue) []*commonpb.KeyValue {
	result := make([]*commonpb.KeyValue, 0, min(len(values), telemetryAttributeLimit))
	for _, value := range values {
		if len(result) >= telemetryAttributeLimit {
			break
		}
		if value == nil || prohibitedTelemetryKey(value.Key) {
			continue
		}
		result = append(result, &commonpb.KeyValue{Key: truncateTelemetryString(value.Key), Value: sanitizeOTLPValue(value.Value)})
	}
	return result
}

func sanitizeOTLPValue(value *commonpb.AnyValue) *commonpb.AnyValue {
	if value == nil {
		return nil
	}
	switch typed := value.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: sanitizeTelemetryString(typed.StringValue)}}
	case *commonpb.AnyValue_BytesValue:
		bytes := typed.BytesValue
		if len(bytes) > telemetryValueLimit {
			bytes = bytes[:telemetryValueLimit]
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: append([]byte(nil), bytes...)}}
	case *commonpb.AnyValue_ArrayValue:
		values := typed.ArrayValue.Values
		if len(values) > telemetryAttributeLimit {
			values = values[:telemetryAttributeLimit]
		}
		result := make([]*commonpb.AnyValue, 0, len(values))
		for _, member := range values {
			result = append(result, sanitizeOTLPValue(member))
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: result}}}
	case *commonpb.AnyValue_KvlistValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: sanitizeOTLPKeyValues(typed.KvlistValue.Values)}}}
	default:
		return protoCloneAnyValue(value)
	}
}

func protoCloneAnyValue(value *commonpb.AnyValue) *commonpb.AnyValue {
	switch typed := value.Value.(type) {
	case *commonpb.AnyValue_BoolValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: typed.BoolValue}}
	case *commonpb.AnyValue_IntValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: typed.IntValue}}
	case *commonpb.AnyValue_DoubleValue:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: typed.DoubleValue}}
	default:
		return &commonpb.AnyValue{}
	}
}

func otlpAttributeIterator(iterator attribute.Iterator) []*commonpb.KeyValue {
	values := make([]attribute.KeyValue, 0, min(iterator.Len(), telemetryAttributeLimit))
	for iterator.Next() && len(values) < telemetryAttributeLimit {
		values = append(values, iterator.Attribute())
	}
	return otlpAttributes(values)
}

func otlpAttributes(values []attribute.KeyValue) []*commonpb.KeyValue {
	result := make([]*commonpb.KeyValue, 0, min(len(values), telemetryAttributeLimit))
	for _, value := range values {
		if len(result) >= telemetryAttributeLimit {
			break
		}
		if prohibitedTelemetryKey(string(value.Key)) {
			continue
		}
		result = append(result, &commonpb.KeyValue{Key: truncateTelemetryString(string(value.Key)), Value: otlpAttributeValue(value.Value)})
	}
	return result
}

func otlpAttributeValue(value attribute.Value) *commonpb.AnyValue {
	result := &commonpb.AnyValue{}
	switch value.Type() {
	case attribute.BOOL:
		result.Value = &commonpb.AnyValue_BoolValue{BoolValue: value.AsBool()}
	case attribute.INT64:
		result.Value = &commonpb.AnyValue_IntValue{IntValue: value.AsInt64()}
	case attribute.FLOAT64:
		result.Value = &commonpb.AnyValue_DoubleValue{DoubleValue: value.AsFloat64()}
	case attribute.STRING:
		result.Value = &commonpb.AnyValue_StringValue{StringValue: sanitizeTelemetryString(value.AsString())}
	case attribute.BYTESLICE:
		bytes := value.AsByteSlice()
		if len(bytes) > telemetryValueLimit {
			bytes = bytes[:telemetryValueLimit]
		}
		result.Value = &commonpb.AnyValue_BytesValue{BytesValue: append([]byte(nil), bytes...)}
	case attribute.BOOLSLICE:
		result.Value = &commonpb.AnyValue_ArrayValue{ArrayValue: otlpBoolValues(value.AsBoolSlice())}
	case attribute.INT64SLICE:
		result.Value = &commonpb.AnyValue_ArrayValue{ArrayValue: otlpIntValues(value.AsInt64Slice())}
	case attribute.FLOAT64SLICE:
		result.Value = &commonpb.AnyValue_ArrayValue{ArrayValue: otlpFloatValues(value.AsFloat64Slice())}
	case attribute.STRINGSLICE:
		result.Value = &commonpb.AnyValue_ArrayValue{ArrayValue: otlpStringValues(value.AsStringSlice())}
	case attribute.SLICE:
		values := value.AsSlice()
		if len(values) > telemetryAttributeLimit {
			values = values[:telemetryAttributeLimit]
		}
		converted := make([]*commonpb.AnyValue, 0, len(values))
		for _, member := range values {
			converted = append(converted, otlpAttributeValue(member))
		}
		result.Value = &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: converted}}
	case attribute.MAP:
		result.Value = &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: otlpAttributes(value.AsMap())}}
	case attribute.EMPTY:
	}
	return result
}

func otlpBoolValues(values []bool) *commonpb.ArrayValue {
	values = values[:min(len(values), telemetryAttributeLimit)]
	result := make([]*commonpb.AnyValue, len(values))
	for i, value := range values {
		result[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value}}
	}
	return &commonpb.ArrayValue{Values: result}
}

func otlpIntValues(values []int64) *commonpb.ArrayValue {
	values = values[:min(len(values), telemetryAttributeLimit)]
	result := make([]*commonpb.AnyValue, len(values))
	for i, value := range values {
		result[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}}
	}
	return &commonpb.ArrayValue{Values: result}
}

func otlpFloatValues(values []float64) *commonpb.ArrayValue {
	values = values[:min(len(values), telemetryAttributeLimit)]
	result := make([]*commonpb.AnyValue, len(values))
	for i, value := range values {
		result[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value}}
	}
	return &commonpb.ArrayValue{Values: result}
}

func otlpStringValues(values []string) *commonpb.ArrayValue {
	values = values[:min(len(values), telemetryAttributeLimit)]
	result := make([]*commonpb.AnyValue, len(values))
	for i, value := range values {
		result[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: sanitizeTelemetryString(value)}}
	}
	return &commonpb.ArrayValue{Values: result}
}

func telemetryUnixNano(value time.Time) uint64 {
	nanoseconds := value.UnixNano()
	if nanoseconds < 0 {
		return 0
	}
	return uint64(nanoseconds)
}

func clampTelemetryUint32(value int) uint32 {
	if value <= 0 {
		return 0
	}
	if int64(value) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(value)
}

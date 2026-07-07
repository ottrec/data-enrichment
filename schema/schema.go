// Package schema holds the protobuf schema for the enrichment output. Date
// and clock encodings follow the scraper's ottrec.v1 schema: dates are
// YYYYMMDDW int32s ([schema.Date] in github.com/ottrec/scraper/schema) and
// clock values are minutes from 00:00 ([schema.ClockTime]).
package schema

//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.66.1 generate --template {"version":"v2","plugins":[{"local":["go","tool","protoc-gen-go"],"out":".","opt":["paths=source_relative","Menrichment.proto=./schema","default_api_level=API_OPAQUE"]}]}

#!/bin/bash

/root/workspace/sentinel-visor/visor \
	--db "postgres://postgres:password@localhost/visor_infos?sslmode=disable" \
	migrate \
	--latest













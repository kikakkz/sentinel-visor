#!/bin/bash

/root/workspace/sentinel-visor/visor --repo "/opt/chain/lotus" \
	--db "postgres://postgres:postgres@localhost/visor_infos?sslmode=disable" \
	walk \
	--tasks "chaineconomics,actorstatesraw,actorstatespower,actorstatesreward,actorstatesminer,actorstatesinit,actorstatesmarket,actorstatesmultisig" 













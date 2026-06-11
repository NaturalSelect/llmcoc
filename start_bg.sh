#!/bin/bash

nohup ./start.sh "$@" > run.log 2>&1 &
PID=$!
trap 'kill -INT $APP_PID 2>/dev/null; wait $APP_PID' EXIT INT
tail -n 1000 -f run.log
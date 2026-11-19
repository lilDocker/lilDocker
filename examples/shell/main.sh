#!/bin/sh
echo "hello from shell ($(basename "$SHELL" 2>/dev/null || echo sh))"

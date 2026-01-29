@echo off
REM Helios 3-Node Cluster Startup Script for Windows

echo Starting Helios 3-Node Cluster...

REM Clean up previous data
echo Cleaning up previous cluster data...
if exist data\node1 rmdir /s /q data\node1
if exist data\node2 rmdir /s /q data\node2
if exist data\node3 rmdir /s /q data\node3

REM Create data directories
mkdir data\node1\raft
mkdir data\node2\raft
mkdir data\node3\raft

REM Build the binary
echo Building helios-atlasd...
go build -o bin\helios-atlasd.exe cmd\helios-atlasd\main.go

REM Start Node 1
echo Starting Node 1 (Leader candidate)...
start /B "Helios Node 1" bin\helios-atlasd.exe -data-dir=data/node1 -listen=:6379 -raft=true -raft-node-id=node-1 -raft-addr=127.0.0.1:7000 -raft-data-dir=data/node1/raft > data\node1\atlasd.log 2>&1

REM Wait for node 1 to initialize
timeout /t 2 /nobreak > nul

REM Start Node 2
echo Starting Node 2...
start /B "Helios Node 2" bin\helios-atlasd.exe -data-dir=data/node2 -listen=:6380 -raft=true -raft-node-id=node-2 -raft-addr=127.0.0.1:7001 -raft-data-dir=data/node2/raft > data\node2\atlasd.log 2>&1

REM Wait for node 2 to initialize
timeout /t 2 /nobreak > nul

REM Start Node 3
echo Starting Node 3...
start /B "Helios Node 3" bin\helios-atlasd.exe -data-dir=data/node3 -listen=:6381 -raft=true -raft-node-id=node-3 -raft-addr=127.0.0.1:7002 -raft-data-dir=data/node3/raft > data\node3\atlasd.log 2>&1

echo.
echo ================================
echo Helios Cluster Started Successfully!
echo ================================
echo.
echo Node 1: redis-cli -p 6379
echo Node 2: redis-cli -p 6380
echo Node 3: redis-cli -p 6381
echo.
echo Logs:
echo   Node 1: type data\node1\atlasd.log
echo   Node 2: type data\node2\atlasd.log
echo   Node 3: type data\node3\atlasd.log
echo.
echo To stop the cluster: scripts\stop-cluster.bat
echo.
echo Waiting for leader election (5 seconds)...
timeout /t 5 /nobreak > nul

echo.
echo Cluster is ready!
pause

#!/usr/bin/env node
// AOF Check and Repair Tool

const fs = require('fs');
const readline = require('readline');

if (process.argv.length < 3) {
    console.error('Usage: node aof-check.js <path-to-aof-file>');
    process.exit(1);
}

const aofPath = process.argv[2];

if (!fs.existsSync(aofPath)) {
    console.error(`Error: File not found: ${aofPath}`);
    process.exit(1);
}

console.log(`Checking AOF file: ${aofPath}`);

const lineReader = readline.createInterface({
    input: fs.createReadStream(aofPath),
    crlfDelay: Infinity
});

let lineNum = 0;
let validLines = 0;
let invalidLines = 0;
const errors = [];

lineReader.on('line', (line) => {
    lineNum++;

    if (line.trim() === '') {
        return; // Skip empty lines
    }

    try {
        const cmd = JSON.parse(line);

        // Validate command structure
        if (!cmd.cmd) {
            throw new Error('Missing cmd field');
        }

        // Validate command types
        const validCommands = ['SET', 'DEL', 'EXPIRE', 'RATE_TAKE', 'JOB_DEQ', 'JOB_ACK', 'JOB_NACK'];
        if (!validCommands.includes(cmd.cmd)) {
            throw new Error(`Unknown command: ${cmd.cmd}`);
        }

        validLines++;
    } catch (e) {
        invalidLines++;
        errors.push({
            line: lineNum,
            content: line.substring(0, 100),
            error: e.message
        });
    }
});

lineReader.on('close', () => {
    console.log('\n=== AOF Check Results ===');
    console.log(`Total lines: ${lineNum}`);
    console.log(`Valid commands: ${validLines}`);
    console.log(`Invalid commands: ${invalidLines}`);

    if (invalidLines > 0) {
        console.log('\n=== Errors Found ===');
        errors.forEach(err => {
            console.log(`Line ${err.line}: ${err.error}`);
            console.log(`  Content: ${err.content}...`);
        });

        console.log('\n[WARNING] AOF file has errors!');
        console.log('Consider truncating to last valid command or restoring from snapshot.');
        process.exit(1);
    } else {
        console.log('\n[OK] AOF file is valid!');
        process.exit(0);
    }
});

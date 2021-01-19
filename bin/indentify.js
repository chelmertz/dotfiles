#!/usr/bin/env nodejs
const {exec} = require('child_process');

const ind = (level) => "  ".repeat(level);

// simple hack for showing nested toString()'d java objects
const print = (str) => {

  // totally disregarding quoting, cross your fingers
  const bracketsIn = '<([';
  const bracketsOut = '>)]';
  let indent = 0;
  let prevNewline = false;
  let out = '';

  for (let i = 0; i<str.length; i++) {
    const c = str.charAt(i);
    if (bracketsIn.includes(c)) {
      indent++;
      out += c + "\n";
      prevNewline = true;
    } else if (bracketsOut.includes(c)) {
      indent--;
      out += "\n" + ind(indent) + c;
      prevNewline = true;
    } else if (c === ',') {
      out += c + "\n";
      prevNewline = true;
    } else if (prevNewline) {
      out += ind(indent);
      if(c !== ' ') {
        // trim leading space for alignment
        out += c;
      }
      prevNewline = false;
    } else {
      out += c;
    }
  }
  return out;
}


if (process.argv.length > 2 && process.argv[2] === 'gui') {
  const zenityCancelExitCode = 1;
  exec('zenity --text-info --editable', (error, stdout, stderr) => {
    if (error && error.code) {
      if (error.code !== zenityCancelExitCode) {
        console.error(`error: ${error}`);
        console.error("Double check that you have installed zenity");
      }
      return;
    }
    console.log(print(stdout.trim()));
  });
} else {
  const text = 'demo<thingy (hello=there)>';
  console.log(print(text));
}

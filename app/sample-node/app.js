const name = process.env.GREETING_NAME || "world";
console.log(`hello ${name} from node ${process.version}`);

let n = 0;
const timer = setInterval(() => {
  console.log(`tick ${++n}`);
  if (n === 3) clearInterval(timer);
}, 500);

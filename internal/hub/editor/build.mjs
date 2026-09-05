import { build } from "esbuild";
await build({
  entryPoints: ["editor.js"],
  bundle: true,
  format: "esm",
  minify: true,
  target: ["es2022"],
  outfile: "../assets/editor.js",
  legalComments: "linked",
});

# Install answer page

`index.html` is a static form. It builds `feldcore-install.json` in the browser and downloads that file. It does not upload the form, and it does not call an analytics service or any other server. The image links are ordinary links to the latest GitHub release.

GitHub Pages can serve this directory from the repository. In the repository settings, open Pages, choose **Deploy from a branch**, select branch `main` and folder `/docs`, and save. The page is then at `https://herculade0.github.io/FELDCORE/install/`.

`docs/.nojekyll` is an empty file so GitHub Pages does not run Jekyll over `docs/`.

The JSON fields are the ones `feldcored` reads: `version`, `hostname`, `timezone`, `network`, `wifi`, `ssh`, `user`, `admin` or `setupCode`, and `dataPool`. `node --test docs/install/answer.test.mjs` checks the generator.

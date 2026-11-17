# Install answer page

`index.html` is a static form. It builds `feldcore-install.json` in the browser and downloads that file. It does not upload the form, and it does not call an analytics service or any other server. The signed OS image stays on the GitHub release page.

GitHub Pages can serve this directory from the repository. In the repository settings, open Pages, choose **Deploy from a branch**, select branch `main` and folder `/docs`, and save. The page is then at `https://herculade0.github.io/FELDCORE/install/`.

`docs/.nojekyll` is an empty file so GitHub Pages does not run Jekyll over `docs/`.

The form writes `version`, `hostname`, `timezone`, `network`, `wifi`, `ssh`, `admin`, and `dataPool`. The time zone defaults to `Australia/Brisbane`. WiFi country is chosen by name and stored as an ISO 3166-1 alpha-2 code, and only when a network name is set. Leave the web administrator username and password blank and the file omits both `admin` and `setupCode`, so the device prints a random setup code on the console. Filling both in writes `admin` with `skipTotp: true`. The form does not write a Unix user or a chosen setup code. Root public-key SSH is unchanged. `node --test docs/install/answer.test.mjs` checks the generator.

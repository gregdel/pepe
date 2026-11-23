{
  lib,
  buildGoModule,
  fetchurl,
  makeWrapper,
  pkg-config,
  runCommand,
  libGL,
  libX11,
  libXcursor,
  libXfixes,
  libxcb,
  libxkbcommon,
  # Fallback player on PATH; null leaves PATH alone (callers pass -mpv).
  mpv,
  vulkan-headers,
  vulkan-loader,
  wayland,
  withX11 ? true,
  withVulkan ? true,
  version ? "dev",
}:

let
  root = ../..;

  # Same SHA-256 pins as build.sh: "SHA256 filename URL" lines, # comments.
  manifest =
    file:
    map
      (
        line:
        let
          fields = lib.splitString " " line;
          name = builtins.elemAt fields 1;
        in
        {
          inherit name;
          file = fetchurl {
            inherit name;
            url = builtins.elemAt fields 2;
            sha256 = builtins.elemAt fields 0;
          };
        }
      )
      (
        builtins.filter (line: line != "" && !lib.hasPrefix "#" line) (
          lib.splitString "\n" (builtins.readFile file)
        )
      );
  copyInto =
    dir: entries: lib.concatMapStrings (e: "install -Dm644 ${e.file} ${dir}/${e.name}\n") entries;

  meta = {
    description = "Desktop, TV and Steam Deck player for movies and shows from a polochon media server";
    homepage = "https://github.com/gregdel/pepe";
    license = lib.licenses.bsd0;
    mainProgram = "pepe";
    platforms = lib.platforms.linux;
  };

  # The Go build depends only on sources and build inputs; runtime paths live
  # in the wrapper below, so changing mpv or the GL loader does not recompile.
  unwrapped = buildGoModule {
    pname = "pepe-unwrapped";
    inherit version meta;

    src = lib.fileset.toSource {
      inherit root;
      fileset = lib.fileset.unions [
        (root + "/go.mod")
        (root + "/go.sum")
        (root + "/main.go")
        (root + "/cmd")
        (root + "/internal")
        (root + "/packaging/assets.go")
        (root + "/packaging/assets.txt")
        (root + "/packaging/linux")
      ];
    };

    vendorHash = "sha256-+Uyo2IFgk3ikY0puoGevppgFScIA+9yXJ8WMw3Q7Zoc=";

    subPackages = [ "." ];
    tags = lib.optional (!withX11) "nox11" ++ lib.optional (!withVulkan) "novulkan";
    ldflags = [
      "-s"
      "-w"
    ];

    nativeBuildInputs = [ pkg-config ];
    buildInputs = [
      libGL
      libxkbcommon
      wayland
    ]
    ++ lib.optionals withX11 [
      libX11
      libXcursor
      libXfixes
      libxcb
    ]
    ++ lib.optional withVulkan vulkan-headers;

    preBuild = ''
      rm -rf internal/app/embed packaging/embed
      ${copyInto "internal/app/embed" (manifest (root + "/packaging/fonts.txt"))}
      ${copyInto "packaging/embed" (manifest (root + "/packaging/assets.txt"))}
    '';

    # Tests need a GPU/EGL stack the build sandbox lacks; CI runs them.
    doCheck = false;

    # share/pepe: CEC keymap, mpv CEC bindings, OSD script and fonts, for
    # integrations that run mpv or ir-keytable outside pépé (pepeOS).
    postInstall = ''
      install -Dm644 packaging/embed/application_icon_1024.png $out/share/pixmaps/pepe.png
      mkdir -p $out/share/applications
      { grep -v '^X-Pepe' packaging/linux/pepe.desktop.in; printf 'Exec=pepe\nIcon=pepe\n'; } \
        > $out/share/applications/pepe.desktop
      install -Dm644 packaging/linux/cec.toml $out/share/pepe/cec.toml
      install -Dm644 packaging/linux/mpv-cec.conf $out/share/pepe/mpv-cec.conf
      install -Dm644 internal/app/mpv/pepe.lua $out/share/pepe/mpv/pepe.lua
      for font in internal/app/embed/*.ttf; do
        install -Dm644 "$font" "$out/share/pepe/fonts/$(basename "$font")"
      done
    '';
  };
in
# Gio dlopens GLES and Vulkan; drivers come from /run/opengl-driver on NixOS.
# mpv is appended so an mpv already on PATH (with user scripts) wins.
runCommand "pepe-${version}"
  {
    nativeBuildInputs = [ makeWrapper ];
    passthru = { inherit unwrapped; };
    inherit meta;
  }
  ''
    mkdir -p $out/bin
    ln -s ${unwrapped}/share $out/share
    makeWrapper ${unwrapped}/bin/pepe $out/bin/pepe --inherit-argv0 \
      --prefix LD_LIBRARY_PATH : ${
        lib.makeLibraryPath ([ libGL ] ++ lib.optional withVulkan vulkan-loader)
      } ${lib.optionalString (mpv != null) "--suffix PATH : ${lib.makeBinPath [ mpv ]}"}
  ''

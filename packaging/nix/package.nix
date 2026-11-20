{
  lib,
  buildGoModule,
  fetchurl,
  makeWrapper,
  pkg-config,
  libGL,
  libX11,
  libXcursor,
  libXfixes,
  libxcb,
  libxkbcommon,
  mpv,
  vulkan-headers,
  vulkan-loader,
  wayland,
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
in
buildGoModule {
  pname = "pepe";
  inherit version;

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

  vendorHash = "sha256-cWs60tU7IQZFOUy0CmBQlxpyaLih+iJDZqNdF3rzt8c=";

  subPackages = [ "." ];
  ldflags = [
    "-s"
    "-w"
  ];

  nativeBuildInputs = [
    makeWrapper
    pkg-config
  ];
  buildInputs = [
    libGL
    libX11
    libXcursor
    libXfixes
    libxcb
    libxkbcommon
    vulkan-headers
    wayland
  ];

  preBuild = ''
    rm -rf internal/app/embed packaging/embed
    ${copyInto "internal/app/embed" (manifest (root + "/packaging/fonts.txt"))}
    ${copyInto "packaging/embed" (manifest (root + "/packaging/assets.txt"))}
  '';

  # Tests need a GPU/EGL stack the build sandbox lacks; CI runs them.
  doCheck = false;

  postInstall = ''
    install -Dm644 packaging/embed/application_icon_1024.png $out/share/pixmaps/pepe.png
    mkdir -p $out/share/applications
    { grep -v '^X-Pepe' packaging/linux/pepe.desktop.in; printf 'Exec=pepe\nIcon=pepe\n'; } \
      > $out/share/applications/pepe.desktop
  '';

  # Gio dlopens GLES and Vulkan; drivers come from /run/opengl-driver on NixOS.
  # mpv is appended so an mpv already on PATH (with user scripts) wins.
  postFixup = ''
    wrapProgram $out/bin/pepe \
      --prefix LD_LIBRARY_PATH : ${
        lib.makeLibraryPath [
          libGL
          vulkan-loader
        ]
      } \
      --suffix PATH : ${lib.makeBinPath [ mpv ]}
  '';

  meta = {
    description = "Desktop, TV and Steam Deck player for movies and shows from a polochon media server";
    homepage = "https://github.com/gregdel/pepe";
    license = lib.licenses.bsd0;
    mainProgram = "pepe";
    platforms = lib.platforms.linux;
  };
}

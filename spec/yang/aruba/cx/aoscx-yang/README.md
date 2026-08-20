<!-- Copyright Hewlett Packard Enterprise Development LP. -->

# AOS-CX YANG Models

This repository contains YANG models for all Aruba OS-CX (AOS-CX) supported platforms, structured to align with OpenConfig YANG models.

## Organization

- Models are organized by AOS-CX OS version as subdirectories.
  - If your AOS-CX version is not explicitly listed, use the closest released version.
- Within each AOS-CX version directory, models are further organized by the corresponding OpenConfig YANG version.
- For more information on OpenConfig YANG versions, refer to the git tags in [OpenConfig/public](https://github.com/openconfig/public).

## Repository Structure

- The directory structure mirrors OpenConfig, with the following exceptions:
  - Third-party dependencies are included within the same directory structure as the OpenConfig models.
  - Each module directory contains only the models relevant to AOS-CX platforms.
  - All deviations from the standard OpenConfig models are listed in the `deviations` directory.

## License

This project is licensed under the [Apache License 2.0](LICENSE).

## Contact

For questions or support, please open an issue in this repository or contact the maintainers.

